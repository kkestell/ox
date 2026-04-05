#include <errno.h>
#include <fcntl.h>
#include <pwd.h>
#include <signal.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/wait.h>
#include <unistd.h>

#if defined(__APPLE__)
#include <util.h>
#else
#include <pty.h>
#endif

#include <SDL3/SDL.h>
#include <SDL3/SDL_main.h>
#include <SDL3_ttf/SDL_ttf.h>
#include <ghostty/vt.h>

#include "boo_tester.h"

#ifndef BOO_PROJECT_DIR
#define BOO_PROJECT_DIR "."
#endif

// ---------------------------------------------------------------------------
// PTY helpers
// ---------------------------------------------------------------------------

typedef struct {
    uint8_t *data;
    size_t len;
    size_t cap;
} BooByteBuffer;

static void boo_byte_buffer_reset(BooByteBuffer *buffer)
{
    if (!buffer)
        return;

    free(buffer->data);
    buffer->data = NULL;
    buffer->len = 0;
    buffer->cap = 0;
}

static bool boo_byte_buffer_append(BooByteBuffer *buffer,
                                   const void *data,
                                   size_t len)
{
    if (!buffer || !data || len == 0)
        return true;

    size_t needed = buffer->len + len;
    if (needed > buffer->cap) {
        size_t new_cap = buffer->cap > 0 ? buffer->cap : 256;
        while (new_cap < needed)
            new_cap *= 2;

        uint8_t *grown = realloc(buffer->data, new_cap);
        if (!grown)
            return false;

        buffer->data = grown;
        buffer->cap = new_cap;
    }

    memcpy(buffer->data + buffer->len, data, len);
    buffer->len += len;
    return true;
}

static char *boo_byte_buffer_snapshot(const BooByteBuffer *buffer, size_t *len_out)
{
    size_t len = buffer ? buffer->len : 0;
    char *copy = malloc(len + 1);
    if (!copy)
        return NULL;

    if (len > 0)
        memcpy(copy, buffer->data, len);
    copy[len] = '\0';
    if (len_out)
        *len_out = len;
    return copy;
}

static void boo_set_errorf(BooSession *session, const char *fmt, ...);
static size_t boo_session_write_pty(BooSession *session,
                                    const void *data,
                                    size_t len);

// Spawn either an explicit command or the user's default shell in a new
// pseudo-terminal.
//
// Creates a pty pair via forkpty(), sets the initial window size, execs the
// shell in the child, and puts the master fd into non-blocking mode so we
// can poll it each frame without stalling the render loop.
//
// Returns the master fd on success (>= 0) and stores the child pid in
// *child_out.  Returns -1 on failure.
static const char *default_shell_path(void)
{
    const char *shell = getenv("SHELL");
    if (!shell || shell[0] == '\0') {
        struct passwd *pw = getpwuid(getuid());
        if (pw && pw->pw_shell && pw->pw_shell[0] != '\0')
            shell = pw->pw_shell;
        else
            shell = "/bin/sh";
    }
    return shell;
}

static void apply_env_overrides(const char *const *env)
{
    if (!env)
        return;

    for (size_t i = 0; env[i]; i++) {
        const char *entry = env[i];
        const char *eq = strchr(entry, '=');
        if (!eq) {
            unsetenv(entry);
            continue;
        }

        size_t key_len = (size_t)(eq - entry);
        if (key_len == 0)
            continue;

        char key[256];
        if (key_len >= sizeof(key))
            key_len = sizeof(key) - 1;
        memcpy(key, entry, key_len);
        key[key_len] = '\0';
        setenv(key, eq + 1, 1);
    }
}

static int pty_spawn(pid_t *child_out, uint16_t cols, uint16_t rows,
                     int cell_width, int cell_height,
                     const char *const *argv,
                     const char *cwd,
                     const char *const *env)
{
    int pty_fd;
    struct winsize ws = {
        .ws_row = rows,
        .ws_col = cols,
        .ws_xpixel = (unsigned short)(cols * cell_width),
        .ws_ypixel = (unsigned short)(rows * cell_height),
    };

    // forkpty() combines openpty + fork + login_tty into one call.
    // In the child it sets up the slave side as stdin/stdout/stderr.
    pid_t child = forkpty(&pty_fd, NULL, NULL, &ws);
    if (child < 0) {
        perror("forkpty");
        return -1;
    }
    if (child == 0) {
        if (cwd && cwd[0] != '\0' && chdir(cwd) != 0) {
            perror("chdir");
            _exit(127);
        }

        apply_env_overrides(env);
        setenv("TERM", "xterm-256color", 1);

        if (argv && argv[0] && argv[0][0] != '\0') {
            execvp(argv[0], (char *const *)argv);
            perror("execvp");
            _exit(127);
        }

        const char *shell = default_shell_path();
        const char *shell_name = strrchr(shell, '/');
        shell_name = shell_name ? shell_name + 1 : shell;
        execl(shell, shell_name, NULL);
        perror("execl");
        _exit(127); // execl only returns on error
    }

    // Parent — make the master fd non-blocking so read() returns EAGAIN
    // instead of blocking when there's no data, letting us poll each frame.
    int flags = fcntl(pty_fd, F_GETFL);
    if (flags < 0 || fcntl(pty_fd, F_SETFL, flags | O_NONBLOCK) < 0) {
        perror("fcntl O_NONBLOCK");
        close(pty_fd);
        return -1;
    }

    *child_out = child;
    return pty_fd;
}

// Best-effort write to the pty master fd.  Because the fd is
// non-blocking, write() may return short or fail with EAGAIN.  We
// retry on EINTR, advance past partial writes, and silently drop
// data if the kernel buffer is full (EAGAIN) — this matches what
// most terminal emulators do under back-pressure.
static size_t pty_write(int pty_fd, const char *buf, size_t len)
{
    size_t total_written = 0;
    size_t test_write_limit = 0;
    const char *test_limit_raw = getenv("BOO_TEST_WRITE_LIMIT");
    if (test_limit_raw && test_limit_raw[0] != '\0') {
        // This test seam lets the smoke suite force a deterministic short
        // write so exact-send error handling is exercised without depending
        // on platform-specific PTY buffer sizes.
        test_write_limit = (size_t)strtoull(test_limit_raw, NULL, 10);
    }

    while (len > 0) {
        size_t chunk_len = len;
        if (test_write_limit > 0) {
            if (total_written >= test_write_limit)
                break;
            size_t remaining_for_test = test_write_limit - total_written;
            if (chunk_len > remaining_for_test)
                chunk_len = remaining_for_test;
        }

        ssize_t n = write(pty_fd, buf, chunk_len);
        if (n > 0) {
            buf += n;
            len -= (size_t)n;
            total_written += (size_t)n;
        } else if (n < 0) {
            if (errno == EINTR)
                continue;
            // EAGAIN or real error — drop the remainder.
            break;
        }
    }

    return total_written;
}

// Result of draining the pty master fd.
typedef enum {
    PTY_READ_OK,    // data was drained (or EAGAIN, i.e. nothing available right now)
    PTY_READ_EOF,   // the child closed its end of the pty
    PTY_READ_ERROR, // a real read error occurred
} PtyReadResult;

// Drain all available output from the pty master and feed it into the
// ghostty terminal.  The terminal's VT parser will process any escape
// sequences and update its internal screen/cursor/style state.
//
// Because the fd is non-blocking, read() returns -1 with EAGAIN once
// the kernel buffer is empty, at which point we stop.
static PtyReadResult pty_read(int pty_fd,
                              GhosttyTerminal terminal,
                              BooByteBuffer *output_buffer,
                              uint64_t *total_output_bytes,
                              uint64_t *last_output_ms)
{
    uint8_t buf[4096];
    for (;;) {
        ssize_t n = read(pty_fd, buf, sizeof(buf));
        if (n > 0) {
            ghostty_terminal_vt_write(terminal, buf, (size_t)n);
            if (total_output_bytes)
                *total_output_bytes += (uint64_t)n;
            if (last_output_ms)
                *last_output_ms = (uint64_t)SDL_GetTicks();
            if (output_buffer)
                boo_byte_buffer_append(output_buffer, buf, (size_t)n);
        } else if (n == 0) {
            // EOF — the child closed its side of the pty.
            return PTY_READ_EOF;
        } else {
            // n == -1: distinguish "no data right now" from real errors.
            if (errno == EAGAIN)
                return PTY_READ_OK;
            if (errno == EINTR)
                continue; // retry the read
            // On Linux, the slave closing often produces EIO rather
            // than a clean EOF (read returning 0).  Treat it the same.
            if (errno == EIO)
                return PTY_READ_EOF;
            perror("pty read");
            return PTY_READ_ERROR;
        }
    }
}

// ---------------------------------------------------------------------------
// Glyph texture cache
// ---------------------------------------------------------------------------

// Simple open-addressing hash map from (codepoint, fg_color, bold, italic)
// to a pre-rendered SDL_Texture.  Avoids re-rasterizing every glyph every
// frame, which is critical for 60fps terminal rendering with SDL_ttf.

#define GLYPH_CACHE_SIZE 4096 // must be a power of two

typedef struct {
    uint32_t codepoint;
    uint32_t fg_packed;   // (r << 16) | (g << 8) | b
    uint8_t  bold;
    uint8_t  italic;
    uint8_t  occupied;
    SDL_Texture *texture;
    int w, h;
} GlyphCacheEntry;

static GlyphCacheEntry glyph_cache[GLYPH_CACHE_SIZE];

static uint32_t glyph_cache_hash(uint32_t cp, uint32_t fg, uint8_t bold, uint8_t italic)
{
    // FNV-1a-ish hash mixing the four key fields.
    uint32_t h = 2166136261u;
    h ^= cp;    h *= 16777619u;
    h ^= fg;    h *= 16777619u;
    h ^= bold;  h *= 16777619u;
    h ^= italic; h *= 16777619u;
    return h & (GLYPH_CACHE_SIZE - 1);
}

// Look up or insert a glyph.  Returns the cached texture (may be NULL on
// first insert — caller must fill it in).
static GlyphCacheEntry *glyph_cache_get(uint32_t cp, uint32_t fg_packed,
                                        uint8_t bold, uint8_t italic)
{
    uint32_t idx = glyph_cache_hash(cp, fg_packed, bold, italic);
    for (uint32_t i = 0; i < GLYPH_CACHE_SIZE; i++) {
        uint32_t slot = (idx + i) & (GLYPH_CACHE_SIZE - 1);
        GlyphCacheEntry *e = &glyph_cache[slot];
        if (!e->occupied) {
            // Empty slot — claim it.
            e->codepoint = cp;
            e->fg_packed = fg_packed;
            e->bold = bold;
            e->italic = italic;
            e->occupied = 1;
            e->texture = NULL;
            return e;
        }
        if (e->codepoint == cp && e->fg_packed == fg_packed
            && e->bold == bold && e->italic == italic) {
            return e;
        }
    }
    // Table full — evict slot 0 as a simple fallback.
    GlyphCacheEntry *e = &glyph_cache[idx];
    if (e->texture)
        SDL_DestroyTexture(e->texture);
    e->codepoint = cp;
    e->fg_packed = fg_packed;
    e->bold = bold;
    e->italic = italic;
    e->texture = NULL;
    return e;
}

static void glyph_cache_clear(void)
{
    for (int i = 0; i < GLYPH_CACHE_SIZE; i++) {
        if (glyph_cache[i].texture)
            SDL_DestroyTexture(glyph_cache[i].texture);
    }
    memset(glyph_cache, 0, sizeof(glyph_cache));
}

// ---------------------------------------------------------------------------
// Input handling
// ---------------------------------------------------------------------------

// Map an SDL scancode to the unshifted US-QWERTY codepoint that the
// Kitty keyboard protocol calls the "base layout key".  This is the
// character the physical key produces on a US layout with no modifiers.
static uint32_t sdl_scancode_to_unshifted_codepoint(SDL_Scancode sc)
{
    if (sc >= SDL_SCANCODE_A && sc <= SDL_SCANCODE_Z)
        return 'a' + (uint32_t)(sc - SDL_SCANCODE_A);
    if (sc >= SDL_SCANCODE_1 && sc <= SDL_SCANCODE_9)
        return '1' + (uint32_t)(sc - SDL_SCANCODE_1);
    if (sc == SDL_SCANCODE_0)
        return '0';

    switch (sc) {
    case SDL_SCANCODE_SPACE:        return ' ';
    case SDL_SCANCODE_MINUS:        return '-';
    case SDL_SCANCODE_EQUALS:       return '=';
    case SDL_SCANCODE_LEFTBRACKET:  return '[';
    case SDL_SCANCODE_RIGHTBRACKET: return ']';
    case SDL_SCANCODE_BACKSLASH:    return '\\';
    case SDL_SCANCODE_SEMICOLON:    return ';';
    case SDL_SCANCODE_APOSTROPHE:   return '\'';
    case SDL_SCANCODE_COMMA:        return ',';
    case SDL_SCANCODE_PERIOD:       return '.';
    case SDL_SCANCODE_SLASH:        return '/';
    case SDL_SCANCODE_GRAVE:        return '`';
    default:                        return 0;
    }
}

// Map an SDL keycode (layout-aware virtual key) to a GhosttyKey.
// Returns GHOSTTY_KEY_UNIDENTIFIED for keys we don't handle.
static GhosttyKey sdl_keycode_to_ghostty(SDL_Keycode sym)
{
    // Letters — SDL3 uses uppercase keycodes (SDLK_A .. SDLK_Z).
    if (sym >= SDLK_A && sym <= SDLK_Z)
        return GHOSTTY_KEY_A + (sym - SDLK_A);

    // Digits.
    if (sym >= SDLK_0 && sym <= SDLK_9)
        return GHOSTTY_KEY_DIGIT_0 + (sym - SDLK_0);

    // Function keys.
    if (sym >= SDLK_F1 && sym <= SDLK_F12)
        return GHOSTTY_KEY_F1 + (sym - SDLK_F1);

    switch (sym) {
    case SDLK_SPACE:        return GHOSTTY_KEY_SPACE;
    case SDLK_RETURN:       return GHOSTTY_KEY_ENTER;
    case SDLK_TAB:          return GHOSTTY_KEY_TAB;
    case SDLK_BACKSPACE:    return GHOSTTY_KEY_BACKSPACE;
    case SDLK_DELETE:       return GHOSTTY_KEY_DELETE;
    case SDLK_ESCAPE:       return GHOSTTY_KEY_ESCAPE;
    case SDLK_UP:           return GHOSTTY_KEY_ARROW_UP;
    case SDLK_DOWN:         return GHOSTTY_KEY_ARROW_DOWN;
    case SDLK_LEFT:         return GHOSTTY_KEY_ARROW_LEFT;
    case SDLK_RIGHT:        return GHOSTTY_KEY_ARROW_RIGHT;
    case SDLK_HOME:         return GHOSTTY_KEY_HOME;
    case SDLK_END:          return GHOSTTY_KEY_END;
    case SDLK_PAGEUP:       return GHOSTTY_KEY_PAGE_UP;
    case SDLK_PAGEDOWN:     return GHOSTTY_KEY_PAGE_DOWN;
    case SDLK_INSERT:       return GHOSTTY_KEY_INSERT;
    case SDLK_MINUS:        return GHOSTTY_KEY_MINUS;
    case SDLK_EQUALS:       return GHOSTTY_KEY_EQUAL;
    case SDLK_LEFTBRACKET:  return GHOSTTY_KEY_BRACKET_LEFT;
    case SDLK_RIGHTBRACKET: return GHOSTTY_KEY_BRACKET_RIGHT;
    case SDLK_BACKSLASH:    return GHOSTTY_KEY_BACKSLASH;
    case SDLK_SEMICOLON:    return GHOSTTY_KEY_SEMICOLON;
    case SDLK_APOSTROPHE:   return GHOSTTY_KEY_QUOTE;
    case SDLK_COMMA:        return GHOSTTY_KEY_COMMA;
    case SDLK_PERIOD:       return GHOSTTY_KEY_PERIOD;
    case SDLK_SLASH:        return GHOSTTY_KEY_SLASH;
    case SDLK_GRAVE:        return GHOSTTY_KEY_BACKQUOTE;
    default:                return GHOSTTY_KEY_UNIDENTIFIED;
    }
}

// Build a GhosttyMods bitmask from SDL's modifier state.
static GhosttyMods sdl_mod_to_ghostty(SDL_Keymod sdl_mod)
{
    GhosttyMods mods = 0;
    if (sdl_mod & SDL_KMOD_SHIFT)
        mods |= GHOSTTY_MODS_SHIFT;
    if (sdl_mod & SDL_KMOD_CTRL)
        mods |= GHOSTTY_MODS_CTRL;
    if (sdl_mod & SDL_KMOD_ALT)
        mods |= GHOSTTY_MODS_ALT;
    if (sdl_mod & SDL_KMOD_GUI)
        mods |= GHOSTTY_MODS_SUPER;
    return mods;
}

// Map an SDL mouse button to a GhosttyMouseButton.
static GhosttyMouseButton sdl_mouse_to_ghostty(uint8_t sdl_button)
{
    switch (sdl_button) {
    case SDL_BUTTON_LEFT:   return GHOSTTY_MOUSE_BUTTON_LEFT;
    case SDL_BUTTON_RIGHT:  return GHOSTTY_MOUSE_BUTTON_RIGHT;
    case SDL_BUTTON_MIDDLE: return GHOSTTY_MOUSE_BUTTON_MIDDLE;
    case SDL_BUTTON_X1:     return GHOSTTY_MOUSE_BUTTON_FOUR;
    case SDL_BUTTON_X2:     return GHOSTTY_MOUSE_BUTTON_FIVE;
    default:                return GHOSTTY_MOUSE_BUTTON_UNKNOWN;
    }
}

// Encode a single Unicode codepoint into a UTF-8 byte buffer.
// Returns the number of bytes written (1–4).
// Invalid codepoints (> U+10FFFF) are replaced with U+FFFD.
static int utf8_encode(uint32_t cp, char out[4])
{
    const uint32_t MAX_UNICODE = 0x10FFFF;
    const uint32_t REPLACEMENT_CHAR = 0xFFFD;

    if (cp > MAX_UNICODE)
        cp = REPLACEMENT_CHAR;

    if (cp < 0x80) {
        out[0] = (char)cp;
        return 1;
    } else if (cp < 0x800) {
        out[0] = (char)(0xC0 | (cp >> 6));
        out[1] = (char)(0x80 | (cp & 0x3F));
        return 2;
    } else if (cp < 0x10000) {
        out[0] = (char)(0xE0 | (cp >> 12));
        out[1] = (char)(0x80 | ((cp >> 6) & 0x3F));
        out[2] = (char)(0x80 | (cp & 0x3F));
        return 3;
    } else {
        out[0] = (char)(0xF0 | (cp >> 18));
        out[1] = (char)(0x80 | ((cp >> 12) & 0x3F));
        out[2] = (char)(0x80 | ((cp >> 6) & 0x3F));
        out[3] = (char)(0x80 | (cp & 0x3F));
        return 4;
    }
}

// Encode a mouse event and write the resulting escape sequence to the pty.
// If the encoder produces no output (e.g. tracking is disabled), this is
// a no-op.
static void mouse_encode_and_write(BooSession *session,
                                   GhosttyMouseEncoder encoder,
                                   GhosttyMouseEvent event)
{
    char buf[128];
    size_t written = 0;
    GhosttyResult res = ghostty_mouse_encoder_encode(
        encoder, event, buf, sizeof(buf), &written);
    if (res == GHOSTTY_SUCCESS && written > 0)
        boo_session_write_pty(session, buf, written);
}

// Process a single SDL key event through the ghostty key encoder and
// write any resulting escape sequence to the pty.
//
// SDL3 gives us everything the Kitty keyboard protocol needs:
//   - scancode:  physical key (→ base layout / unshifted codepoint)
//   - key:       layout-aware virtual key (→ GhosttyKey)
//   - mod:       modifier bitmask at event time
//   - repeat:    distinguishes press from repeat
//   - SDL_EVENT_TEXT_INPUT: UTF-8 text, delivered separately
//
// The text_utf8/text_utf8_len come from a SDL_TEXTINPUT event that was
// collected during the same frame's event loop.
static void handle_key_event(BooSession *session,
                             GhosttyKeyEncoder encoder,
                             GhosttyKeyEvent event,
                             const SDL_KeyboardEvent *sdl_key,
                             const char *text_utf8, int text_utf8_len)
{
    GhosttyKey gkey = sdl_keycode_to_ghostty(sdl_key->key);
    if (gkey == GHOSTTY_KEY_UNIDENTIFIED)
        return;

    GhosttyKeyAction action;
    if (!sdl_key->down)
        action = GHOSTTY_KEY_ACTION_RELEASE;
    else if (sdl_key->repeat)
        action = GHOSTTY_KEY_ACTION_REPEAT;
    else
        action = GHOSTTY_KEY_ACTION_PRESS;

    GhosttyMods mods = sdl_mod_to_ghostty(sdl_key->mod);

    ghostty_key_event_set_key(event, gkey);
    ghostty_key_event_set_action(event, action);
    ghostty_key_event_set_mods(event, mods);

    // The unshifted codepoint is derived from the physical scancode —
    // the character the key produces on a US QWERTY layout with no
    // modifiers.  The Kitty protocol uses this as the base layout key.
    uint32_t ucp = sdl_scancode_to_unshifted_codepoint(sdl_key->scancode);
    ghostty_key_event_set_unshifted_codepoint(event, ucp);

    // Consumed mods: modifiers that the OS text input system already
    // accounted for when producing the UTF-8 text.  For printable keys
    // with text, shift is consumed (it turns 'a' → 'A').
    GhosttyMods consumed = 0;
    if (ucp != 0 && text_utf8_len > 0 && (mods & GHOSTTY_MODS_SHIFT))
        consumed |= GHOSTTY_MODS_SHIFT;
    ghostty_key_event_set_consumed_mods(event, consumed);

    // Attach UTF-8 text if available.  Release events never carry text.
    if (text_utf8_len > 0 && action != GHOSTTY_KEY_ACTION_RELEASE)
        ghostty_key_event_set_utf8(event, text_utf8, (size_t)text_utf8_len);
    else
        ghostty_key_event_set_utf8(event, NULL, 0);

    char buf[128];
    size_t written = 0;
    GhosttyResult res = ghostty_key_encoder_encode(
        encoder, event, buf, sizeof(buf), &written);
    if (res == GHOSTTY_SUCCESS && written > 0)
        boo_session_write_pty(session, buf, written);
}

// Handle scrollbar drag-to-scroll.  When the user clicks in the
// scrollbar region we begin tracking; while held we map the mouse Y
// position directly to an absolute scroll offset so the thumb follows
// the cursor exactly.
//
// Returns true while a drag is in progress so the caller can skip
// normal mouse handling if desired.
static bool handle_scrollbar(GhosttyTerminal terminal,
                             GhosttyRenderState render_state,
                             bool *dragging, int scr_w, int scr_h,
                             int mx, int my, bool pressed, bool held,
                             bool released)
{
    // Query scrollbar geometry from the terminal.
    GhosttyTerminalScrollbar scrollbar = {0};
    if (ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR,
                             &scrollbar) != GHOSTTY_SUCCESS)
        return false;

    // Nothing to drag when the viewport covers all content.
    if (scrollbar.total <= scrollbar.len) {
        *dragging = false;
        return false;
    }

    const int bar_width = 6;
    const int bar_margin = 2;
    int bar_left = scr_w - bar_width - bar_margin;
    // Use a wider hit region for easier grabbing.
    int hit_left = bar_left - 8;

    // Start a drag when the user clicks inside the hit region.
    if (pressed && mx >= hit_left && mx <= scr_w)
        *dragging = true;

    if (*dragging && held) {
        // Map mouse Y directly to an absolute scroll offset.
        uint64_t scrollable = scrollbar.total - scrollbar.len;
        double frac = (double)my / (double)scr_h;
        if (frac < 0.0) frac = 0.0;
        if (frac > 1.0) frac = 1.0;
        int64_t target = (int64_t)(frac * (double)scrollable);

        intptr_t delta = (intptr_t)(target - (int64_t)scrollbar.offset);
        if (delta != 0) {
            GhosttyTerminalScrollViewport sv = {
                .tag = GHOSTTY_SCROLL_VIEWPORT_DELTA,
                .value = { .delta = delta },
            };
            ghostty_terminal_scroll_viewport(terminal, sv);
            ghostty_render_state_update(render_state, terminal);
        }
    }

    if (released)
        *dragging = false;

    return *dragging;
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// Render a single glyph at (x, y) using the glyph cache.  Rasterizes
// with SDL_ttf on first use, then reuses the cached texture.
static void render_glyph(SDL_Renderer *renderer, TTF_Font *font,
                         uint32_t codepoint, int x, int y,
                         SDL_Color fg, uint8_t bold, uint8_t italic,
                         int cell_width, int cell_height,
                         float dpi_scale_x, float dpi_scale_y)
{
    uint32_t fg_packed = ((uint32_t)fg.r << 16) | ((uint32_t)fg.g << 8) | fg.b;
    GlyphCacheEntry *entry = glyph_cache_get(codepoint, fg_packed, bold, italic);

    if (!entry->texture) {
        SDL_Surface *surf = TTF_RenderGlyph_Blended(font, codepoint, fg);
        if (!surf)
            return;
        entry->texture = SDL_CreateTextureFromSurface(renderer, surf);
        entry->w = surf->w;
        entry->h = surf->h;
        SDL_DestroySurface(surf);
        if (!entry->texture)
            return;
    }

    // Texture is at native pixel resolution; convert to logical points.
    int lw = (int)(entry->w / dpi_scale_x);
    int lh = (int)(entry->h / dpi_scale_y);
    SDL_FRect dst = { (float)x, (float)y, (float)lw, (float)lh };
    if (dst.w > cell_width) dst.w = (float)cell_width;
    if (dst.h > cell_height) dst.h = (float)cell_height;
    SDL_RenderTexture(renderer, entry->texture, NULL, &dst);
}

// Render the current terminal screen using the RenderState API.
//
// For each row/cell we read the grapheme codepoints and the cell's style,
// resolve foreground/background colors via the palette, and draw each
// character individually.  This supports per-cell colors from SGR
// sequences (bold, 256-color, 24-bit RGB, etc.).
static void render_terminal(SDL_Renderer *renderer,
                            TTF_Font *regular_font,
                            TTF_Font *bold_font,
                            TTF_Font *italic_font,
                            TTF_Font *bold_italic_font,
                            GhosttyRenderState render_state,
                            GhosttyRenderStateRowIterator row_iter,
                            GhosttyRenderStateRowCells cells,
                            int cell_width, int cell_height,
                            int pad,
                            const GhosttyTerminalScrollbar *scrollbar,
                            int scr_w, int scr_h,
                            float dpi_scale_x, float dpi_scale_y)
{
    // Grab colors (palette, default fg/bg) from the render state so we
    // can resolve palette-indexed cell colors.
    GhosttyRenderStateColors colors = GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
    if (ghostty_render_state_colors_get(render_state, &colors) != GHOSTTY_SUCCESS)
        return;

    // Populate the row iterator from the current render state snapshot.
    if (ghostty_render_state_get(render_state,
            GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &row_iter) != GHOSTTY_SUCCESS)
        return;

    int y = pad;

    while (ghostty_render_state_row_iterator_next(row_iter)) {
        if (ghostty_render_state_row_get(row_iter,
                GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &cells) != GHOSTTY_SUCCESS)
            continue;

        int x = pad;

        while (ghostty_render_state_row_cells_next(cells)) {
            uint32_t grapheme_len = 0;
            ghostty_render_state_row_cells_get(cells,
                GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &grapheme_len);

            if (grapheme_len == 0) {
                // Empty cell — may still have a background color.
                GhosttyColorRgb bg = {0};
                if (ghostty_render_state_row_cells_get(cells,
                        GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &bg) == GHOSTTY_SUCCESS) {
                    SDL_SetRenderDrawColor(renderer, bg.r, bg.g, bg.b, 255);
                    SDL_FRect r = { (float)x, (float)y, (float)cell_width, (float)cell_height };
                    SDL_RenderFillRect(renderer, &r);
                }
                x += cell_width;
                continue;
            }

            // Read the grapheme codepoints.
            uint32_t codepoints[16];
            uint32_t len = grapheme_len < 16 ? grapheme_len : 16;
            ghostty_render_state_row_cells_get(cells,
                GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, codepoints);

            // Resolve foreground and background colors.
            GhosttyColorRgb fg = colors.foreground;
            ghostty_render_state_row_cells_get(cells,
                GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &fg);

            GhosttyColorRgb bg_rgb = colors.background;
            bool has_bg = ghostty_render_state_row_cells_get(cells,
                GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &bg_rgb) == GHOSTTY_SUCCESS;

            // Read the style for flags (inverse, bold, italic).
            GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
            ghostty_render_state_row_cells_get(cells,
                GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);

            // Inverse (reverse video): swap foreground and background.
            if (style.inverse) {
                GhosttyColorRgb tmp = fg;
                fg = bg_rgb;
                bg_rgb = tmp;
                has_bg = true;
            }

            // Draw background rectangle if cell has non-default bg.
            if (has_bg) {
                SDL_SetRenderDrawColor(renderer, bg_rgb.r, bg_rgb.g, bg_rgb.b, 255);
                SDL_FRect r = { (float)x, (float)y, (float)cell_width, (float)cell_height };
                SDL_RenderFillRect(renderer, &r);
            }

            SDL_Color sdl_fg = { fg.r, fg.g, fg.b, 255 };

            // Render each codepoint in the grapheme.
            // For simple single-codepoint cells this is just one glyph.
            // Multi-codepoint graphemes (combining chars, emoji sequences)
            // are rendered as individual glyphs stacked at the same position.
            for (uint32_t i = 0; i < len; i++) {
                TTF_Font *font = regular_font;
                if (style.bold && style.italic && bold_italic_font)
                    font = bold_italic_font;
                else if (style.bold && bold_font)
                    font = bold_font;
                else if (style.italic && italic_font)
                    font = italic_font;

                render_glyph(renderer, font, codepoints[i],
                             x, y, sdl_fg,
                             style.bold, style.italic,
                             cell_width, cell_height,
                             dpi_scale_x, dpi_scale_y);
            }

            x += cell_width;
        }

        // Clear per-row dirty flag after rendering.
        bool clean = false;
        ghostty_render_state_row_set(row_iter,
            GHOSTTY_RENDER_STATE_ROW_OPTION_DIRTY, &clean);

        y += cell_height;
    }

    // Draw the cursor.
    bool cursor_visible = false;
    ghostty_render_state_get(render_state,
        GHOSTTY_RENDER_STATE_DATA_CURSOR_VISIBLE, &cursor_visible);
    bool cursor_in_viewport = false;
    ghostty_render_state_get(render_state,
        GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_HAS_VALUE, &cursor_in_viewport);

    if (cursor_visible && cursor_in_viewport) {
        uint16_t cx = 0, cy = 0;
        ghostty_render_state_get(render_state,
            GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_X, &cx);
        ghostty_render_state_get(render_state,
            GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_Y, &cy);

        GhosttyColorRgb cur_rgb = colors.foreground;
        if (colors.cursor_has_value)
            cur_rgb = colors.cursor;
        int cur_x = pad + cx * cell_width;
        int cur_y = pad + cy * cell_height;
        SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_BLEND);
        SDL_SetRenderDrawColor(renderer, cur_rgb.r, cur_rgb.g, cur_rgb.b, 128);
        SDL_FRect cursor_rect = { (float)cur_x, (float)cur_y, (float)cell_width, (float)cell_height };
        SDL_RenderFillRect(renderer, &cursor_rect);
        SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_NONE);
    }

    // Draw the scrollbar when there is scrollback content to scroll through.
    if (scrollbar && scrollbar->total > scrollbar->len) {
        const int bar_width = 6;
        const int bar_margin = 2;
        int bar_x = scr_w - bar_width - bar_margin;

        double visible_frac = (double)scrollbar->len / (double)scrollbar->total;
        int thumb_height = (int)(scr_h * visible_frac);
        if (thumb_height < 10) thumb_height = 10;

        double scroll_frac = (scrollbar->total > scrollbar->len)
            ? (double)scrollbar->offset / (double)(scrollbar->total - scrollbar->len)
            : 1.0;
        int thumb_y = (int)(scroll_frac * (scr_h - thumb_height));

        SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_BLEND);
        SDL_SetRenderDrawColor(renderer, 200, 200, 200, 128);
        SDL_FRect bar_rect = { (float)bar_x, (float)thumb_y, (float)bar_width, (float)thumb_height };
        SDL_RenderFillRect(renderer, &bar_rect);
        SDL_SetRenderDrawBlendMode(renderer, SDL_BLENDMODE_NONE);
    }

    // Reset global dirty state so the next update reports changes accurately.
    GhosttyRenderStateDirty clean_state = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
    ghostty_render_state_set(render_state,
        GHOSTTY_RENDER_STATE_OPTION_DIRTY, &clean_state);
}

// ---------------------------------------------------------------------------
// Build info
// ---------------------------------------------------------------------------

// Log compile-time build info from libghostty-vt so we can quickly tell
// whether the library was built with SIMD, and in which optimization mode.
static void log_build_info(void)
{
    bool simd = false;
    ghostty_build_info(GHOSTTY_BUILD_INFO_SIMD, &simd);

    GhosttyOptimizeMode opt = GHOSTTY_OPTIMIZE_DEBUG;
    ghostty_build_info(GHOSTTY_BUILD_INFO_OPTIMIZE, &opt);

    const char *opt_str;
    switch (opt) {
    case GHOSTTY_OPTIMIZE_DEBUG:        opt_str = "Debug";        break;
    case GHOSTTY_OPTIMIZE_RELEASE_SAFE: opt_str = "ReleaseSafe";  break;
    case GHOSTTY_OPTIMIZE_RELEASE_SMALL: opt_str = "ReleaseSmall"; break;
    case GHOSTTY_OPTIMIZE_RELEASE_FAST: opt_str = "ReleaseFast";  break;
    default:                            opt_str = "Unknown";       break;
    }

    SDL_Log("ghostty-vt: simd:     %s", simd ? "enabled" : "disabled");
    SDL_Log("ghostty-vt: optimize: %s", opt_str);
}

// ---------------------------------------------------------------------------
// Effects callbacks
// ---------------------------------------------------------------------------

// Context passed through the terminal's userdata pointer to all effect
// callbacks so they can reach the pty fd (and anything else they need)
// without global state.
typedef struct {
    BooSession *session;
    int pty_fd;
    int cell_width;
    int cell_height;
    uint16_t cols;
    uint16_t rows;
    SDL_Window *window;   // for SetWindowTitle
} EffectsContext;

struct BooSession {
    int font_size;
    int pad;
    int cell_width;
    int cell_height;
    int scr_w;
    int scr_h;
    float dpi_scale_x;
    float dpi_scale_y;
    uint16_t term_cols;
    uint16_t term_rows;
    SDL_Window *window;
    SDL_Renderer *renderer;
    TTF_Font *mono_font;
    TTF_Font *mono_font_bold;
    TTF_Font *mono_font_italic;
    TTF_Font *mono_font_bold_italic;
    char *font_regular_path;
    char *font_bold_path;
    char *font_italic_path;
    char *font_bold_italic_path;
    GhosttyTerminal terminal;
    pid_t child;
    int pty_fd;
    GhosttyKeyEncoder key_encoder;
    GhosttyKeyEvent key_event;
    GhosttyMouseEncoder mouse_encoder;
    GhosttyMouseEvent mouse_event;
    GhosttyRenderState render_state;
    GhosttyRenderStateRowIterator row_iter;
    GhosttyRenderStateRowCells row_cells;
    EffectsContext effects_ctx;
    BooByteBuffer input_bytes;
    BooByteBuffer output_bytes;
    uint64_t total_input_bytes;
    uint64_t total_output_bytes;
    uint64_t last_input_ms;
    uint64_t last_output_ms;
    uint16_t scripted_mouse_buttons;
    bool capture_output;
    bool sdl_acquired;
    bool text_input_started;
    bool launched;
    bool visible;
    bool prev_focused;
    bool scrollbar_dragging;
    bool child_exited;
    bool child_reaped;
    int child_exit_status;
    char last_error[512];
};

static int s_sdl_refcount = 0;
static bool s_logged_build_info = false;

static uint64_t boo_now_ms(void)
{
    return (uint64_t)SDL_GetTicks();
}

static void boo_session_note_input(BooSession *session,
                                   const void *data,
                                   size_t len)
{
    if (!session || !data || len == 0)
        return;

    boo_byte_buffer_append(&session->input_bytes, data, len);
    session->total_input_bytes += len;
    session->last_input_ms = boo_now_ms();
}

static size_t boo_session_write_pty(BooSession *session,
                                    const void *data,
                                    size_t len)
{
    if (!session || !data || len == 0)
        return 0;

    size_t written = pty_write(session->pty_fd, (const char *)data, len);
    if (written > 0)
        boo_session_note_input(session, data, written);
    return written;
}

static int boo_session_require_full_write(BooSession *session,
                                          size_t written,
                                          size_t expected,
                                          const char *operation)
{
    if (written == expected)
        return 0;

    boo_set_errorf(session,
                   "%s wrote %zu of %zu bytes; the PTY back-pressured the send",
                   operation,
                   written,
                   expected);
    return -1;
}

static void boo_clear_error(BooSession *session)
{
    if (session)
        session->last_error[0] = '\0';
}

static void boo_set_errorf(BooSession *session, const char *fmt, ...)
{
    if (!session)
        return;

    va_list ap;
    va_start(ap, fmt);
    vsnprintf(session->last_error, sizeof(session->last_error), fmt, ap);
    va_end(ap);
}

static int boo_session_mouse_position(BooSession *session,
                                      int x,
                                      int y,
                                      GhosttyMousePosition *out_position)
{
    if (x < 0 || y < 0 || x >= session->term_cols || y >= session->term_rows) {
        boo_set_errorf(session,
                       "mouse coordinates (%d, %d) must be within the terminal grid",
                       x,
                       y);
        return -1;
    }

    out_position->x = (float)(session->pad + x * session->cell_width
                              + session->cell_width / 2);
    out_position->y = (float)(session->pad + y * session->cell_height
                              + session->cell_height / 2);
    return 0;
}

static void boo_session_configure_mouse_encoder(BooSession *session,
                                                GhosttyMousePosition position,
                                                GhosttyMods mods)
{
    ghostty_mouse_encoder_setopt_from_terminal(session->mouse_encoder,
                                               session->terminal);
    ghostty_mouse_event_set_mods(session->mouse_event, mods);
    ghostty_mouse_event_set_position(session->mouse_event, position);

    GhosttyMouseEncoderSize enc_size = {
        .size = sizeof(GhosttyMouseEncoderSize),
        .screen_width = (uint32_t)session->scr_w,
        .screen_height = (uint32_t)session->scr_h,
        .cell_width = (uint32_t)session->cell_width,
        .cell_height = (uint32_t)session->cell_height,
        .padding_top = (uint32_t)session->pad,
        .padding_bottom = (uint32_t)session->pad,
        .padding_left = (uint32_t)session->pad,
        .padding_right = (uint32_t)session->pad,
    };
    ghostty_mouse_encoder_setopt(session->mouse_encoder,
        GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &enc_size);

    bool any_pressed = session->scripted_mouse_buttons != 0;
    ghostty_mouse_encoder_setopt(session->mouse_encoder,
        GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, &any_pressed);

    bool track_cell = true;
    ghostty_mouse_encoder_setopt(session->mouse_encoder,
        GHOSTTY_MOUSE_ENCODER_OPT_TRACK_LAST_CELL, &track_cell);
}

static int boo_sdl_acquire(BooSession *session)
{
    if (!session)
        return -1;
    if (session->sdl_acquired)
        return 0;

    if (s_sdl_refcount == 0) {
        if (!SDL_Init(SDL_INIT_VIDEO)) {
            boo_set_errorf(session, "SDL_Init failed: %s", SDL_GetError());
            return -1;
        }
        if (!TTF_Init()) {
            boo_set_errorf(session, "TTF_Init failed: %s", SDL_GetError());
            SDL_Quit();
            return -1;
        }
        if (!s_logged_build_info) {
            log_build_info();
            s_logged_build_info = true;
        }
    }

    s_sdl_refcount++;
    session->sdl_acquired = true;
    return 0;
}

static void boo_sdl_release(BooSession *session)
{
    if (!session || !session->sdl_acquired)
        return;

    session->sdl_acquired = false;
    if (s_sdl_refcount > 0)
        s_sdl_refcount--;
    if (s_sdl_refcount == 0) {
        TTF_Quit();
        SDL_Quit();
    }
}

static GhosttyKey boo_key_to_ghostty(BooKey key)
{
    if (key >= BOO_KEY_A && key <= BOO_KEY_Z)
        return GHOSTTY_KEY_A + (key - BOO_KEY_A);
    if (key >= BOO_KEY_DIGIT_0 && key <= BOO_KEY_DIGIT_9)
        return GHOSTTY_KEY_DIGIT_0 + (key - BOO_KEY_DIGIT_0);
    if (key >= BOO_KEY_F1 && key <= BOO_KEY_F12)
        return GHOSTTY_KEY_F1 + (key - BOO_KEY_F1);

    switch (key) {
    case BOO_KEY_BACKQUOTE:      return GHOSTTY_KEY_BACKQUOTE;
    case BOO_KEY_BACKSLASH:      return GHOSTTY_KEY_BACKSLASH;
    case BOO_KEY_BRACKET_LEFT:   return GHOSTTY_KEY_BRACKET_LEFT;
    case BOO_KEY_BRACKET_RIGHT:  return GHOSTTY_KEY_BRACKET_RIGHT;
    case BOO_KEY_COMMA:          return GHOSTTY_KEY_COMMA;
    case BOO_KEY_EQUAL:          return GHOSTTY_KEY_EQUAL;
    case BOO_KEY_MINUS:          return GHOSTTY_KEY_MINUS;
    case BOO_KEY_PERIOD:         return GHOSTTY_KEY_PERIOD;
    case BOO_KEY_QUOTE:          return GHOSTTY_KEY_QUOTE;
    case BOO_KEY_SEMICOLON:      return GHOSTTY_KEY_SEMICOLON;
    case BOO_KEY_SLASH:          return GHOSTTY_KEY_SLASH;
    case BOO_KEY_BACKSPACE:      return GHOSTTY_KEY_BACKSPACE;
    case BOO_KEY_ENTER:          return GHOSTTY_KEY_ENTER;
    case BOO_KEY_SPACE:          return GHOSTTY_KEY_SPACE;
    case BOO_KEY_TAB:            return GHOSTTY_KEY_TAB;
    case BOO_KEY_DELETE:         return GHOSTTY_KEY_DELETE;
    case BOO_KEY_END:            return GHOSTTY_KEY_END;
    case BOO_KEY_HOME:           return GHOSTTY_KEY_HOME;
    case BOO_KEY_INSERT:         return GHOSTTY_KEY_INSERT;
    case BOO_KEY_PAGE_DOWN:      return GHOSTTY_KEY_PAGE_DOWN;
    case BOO_KEY_PAGE_UP:        return GHOSTTY_KEY_PAGE_UP;
    case BOO_KEY_ARROW_DOWN:     return GHOSTTY_KEY_ARROW_DOWN;
    case BOO_KEY_ARROW_LEFT:     return GHOSTTY_KEY_ARROW_LEFT;
    case BOO_KEY_ARROW_RIGHT:    return GHOSTTY_KEY_ARROW_RIGHT;
    case BOO_KEY_ARROW_UP:       return GHOSTTY_KEY_ARROW_UP;
    case BOO_KEY_ESCAPE:         return GHOSTTY_KEY_ESCAPE;
    default:                           return GHOSTTY_KEY_UNIDENTIFIED;
    }
}

static GhosttyMods boo_mods_to_ghostty(uint16_t modifiers)
{
    GhosttyMods mods = 0;
    if (modifiers & BOO_MOD_SHIFT)
        mods |= GHOSTTY_MODS_SHIFT;
    if (modifiers & BOO_MOD_CTRL)
        mods |= GHOSTTY_MODS_CTRL;
    if (modifiers & BOO_MOD_ALT)
        mods |= GHOSTTY_MODS_ALT;
    if (modifiers & BOO_MOD_SUPER)
        mods |= GHOSTTY_MODS_SUPER;
    return mods;
}

enum {
    BOO_MOUSE_MASK_LEFT = 1 << 0,
    BOO_MOUSE_MASK_RIGHT = 1 << 1,
    BOO_MOUSE_MASK_MIDDLE = 1 << 2,
    BOO_MOUSE_MASK_X1 = 1 << 3,
    BOO_MOUSE_MASK_X2 = 1 << 4,
};

static GhosttyMouseButton boo_mouse_button_to_ghostty(BooMouseButton button)
{
    switch (button) {
    case BOO_MOUSE_BUTTON_LEFT:   return GHOSTTY_MOUSE_BUTTON_LEFT;
    case BOO_MOUSE_BUTTON_RIGHT:  return GHOSTTY_MOUSE_BUTTON_RIGHT;
    case BOO_MOUSE_BUTTON_MIDDLE: return GHOSTTY_MOUSE_BUTTON_MIDDLE;
    case BOO_MOUSE_BUTTON_X1:     return GHOSTTY_MOUSE_BUTTON_FOUR;
    case BOO_MOUSE_BUTTON_X2:     return GHOSTTY_MOUSE_BUTTON_FIVE;
    default:                      return GHOSTTY_MOUSE_BUTTON_UNKNOWN;
    }
}

static uint16_t boo_mouse_button_to_mask(BooMouseButton button)
{
    switch (button) {
    case BOO_MOUSE_BUTTON_LEFT:   return BOO_MOUSE_MASK_LEFT;
    case BOO_MOUSE_BUTTON_RIGHT:  return BOO_MOUSE_MASK_RIGHT;
    case BOO_MOUSE_BUTTON_MIDDLE: return BOO_MOUSE_MASK_MIDDLE;
    case BOO_MOUSE_BUTTON_X1:     return BOO_MOUSE_MASK_X1;
    case BOO_MOUSE_BUTTON_X2:     return BOO_MOUSE_MASK_X2;
    default:                      return 0;
    }
}

static GhosttyMouseButton boo_mouse_primary_button(uint16_t buttons)
{
    if (buttons & BOO_MOUSE_MASK_LEFT)
        return GHOSTTY_MOUSE_BUTTON_LEFT;
    if (buttons & BOO_MOUSE_MASK_RIGHT)
        return GHOSTTY_MOUSE_BUTTON_RIGHT;
    if (buttons & BOO_MOUSE_MASK_MIDDLE)
        return GHOSTTY_MOUSE_BUTTON_MIDDLE;
    if (buttons & BOO_MOUSE_MASK_X1)
        return GHOSTTY_MOUSE_BUTTON_FOUR;
    if (buttons & BOO_MOUSE_MASK_X2)
        return GHOSTTY_MOUSE_BUTTON_FIVE;
    return GHOSTTY_MOUSE_BUTTON_UNKNOWN;
}

static uint32_t boo_key_to_unshifted_codepoint(BooKey key)
{
    if (key >= BOO_KEY_A && key <= BOO_KEY_Z)
        return 'a' + (uint32_t)(key - BOO_KEY_A);
    if (key >= BOO_KEY_DIGIT_0 && key <= BOO_KEY_DIGIT_9)
        return '0' + (uint32_t)(key - BOO_KEY_DIGIT_0);

    switch (key) {
    case BOO_KEY_BACKQUOTE:     return '`';
    case BOO_KEY_BACKSLASH:     return '\\';
    case BOO_KEY_BRACKET_LEFT:  return '[';
    case BOO_KEY_BRACKET_RIGHT: return ']';
    case BOO_KEY_COMMA:         return ',';
    case BOO_KEY_EQUAL:         return '=';
    case BOO_KEY_MINUS:         return '-';
    case BOO_KEY_PERIOD:        return '.';
    case BOO_KEY_QUOTE:         return '\'';
    case BOO_KEY_SEMICOLON:     return ';';
    case BOO_KEY_SLASH:         return '/';
    case BOO_KEY_SPACE:         return ' ';
    default:                          return 0;
    }
}

static uint32_t boo_key_to_shifted_codepoint(BooKey key)
{
    if (key >= BOO_KEY_A && key <= BOO_KEY_Z)
        return 'A' + (uint32_t)(key - BOO_KEY_A);

    switch (key) {
    case BOO_KEY_DIGIT_0:       return ')';
    case BOO_KEY_DIGIT_1:       return '!';
    case BOO_KEY_DIGIT_2:       return '@';
    case BOO_KEY_DIGIT_3:       return '#';
    case BOO_KEY_DIGIT_4:       return '$';
    case BOO_KEY_DIGIT_5:       return '%';
    case BOO_KEY_DIGIT_6:       return '^';
    case BOO_KEY_DIGIT_7:       return '&';
    case BOO_KEY_DIGIT_8:       return '*';
    case BOO_KEY_DIGIT_9:       return '(';
    case BOO_KEY_BACKQUOTE:     return '~';
    case BOO_KEY_BACKSLASH:     return '|';
    case BOO_KEY_BRACKET_LEFT:  return '{';
    case BOO_KEY_BRACKET_RIGHT: return '}';
    case BOO_KEY_COMMA:         return '<';
    case BOO_KEY_EQUAL:         return '+';
    case BOO_KEY_MINUS:         return '_';
    case BOO_KEY_PERIOD:        return '>';
    case BOO_KEY_QUOTE:         return '"';
    case BOO_KEY_SEMICOLON:     return ':';
    case BOO_KEY_SLASH:         return '?';
    case BOO_KEY_SPACE:         return ' ';
    default:                          return boo_key_to_unshifted_codepoint(key);
    }
}

static void boo_session_cleanup(BooSession *session)
{
    if (!session)
        return;

    glyph_cache_clear();
    boo_byte_buffer_reset(&session->input_bytes);
    boo_byte_buffer_reset(&session->output_bytes);

    if (session->text_input_started && session->window) {
        SDL_StopTextInput(session->window);
        session->text_input_started = false;
    }
    if (session->mono_font) {
        TTF_CloseFont(session->mono_font);
        session->mono_font = NULL;
    }
    if (session->mono_font_bold) {
        TTF_CloseFont(session->mono_font_bold);
        session->mono_font_bold = NULL;
    }
    if (session->mono_font_italic) {
        TTF_CloseFont(session->mono_font_italic);
        session->mono_font_italic = NULL;
    }
    if (session->mono_font_bold_italic) {
        TTF_CloseFont(session->mono_font_bold_italic);
        session->mono_font_bold_italic = NULL;
    }
    free(session->font_regular_path);
    session->font_regular_path = NULL;
    free(session->font_bold_path);
    session->font_bold_path = NULL;
    free(session->font_italic_path);
    session->font_italic_path = NULL;
    free(session->font_bold_italic_path);
    session->font_bold_italic_path = NULL;
    if (session->renderer) {
        SDL_DestroyRenderer(session->renderer);
        session->renderer = NULL;
    }
    if (session->window) {
        SDL_DestroyWindow(session->window);
        session->window = NULL;
    }
    if (session->pty_fd >= 0) {
        close(session->pty_fd);
        session->pty_fd = -1;
    }
    if (session->child > 0 && !session->child_reaped) {
        if (!session->child_exited)
            kill(session->child, SIGHUP);
        waitpid(session->child, NULL, 0);
    }

    if (session->mouse_event) {
        ghostty_mouse_event_free(session->mouse_event);
        session->mouse_event = NULL;
    }
    if (session->mouse_encoder) {
        ghostty_mouse_encoder_free(session->mouse_encoder);
        session->mouse_encoder = NULL;
    }
    if (session->key_event) {
        ghostty_key_event_free(session->key_event);
        session->key_event = NULL;
    }
    if (session->key_encoder) {
        ghostty_key_encoder_free(session->key_encoder);
        session->key_encoder = NULL;
    }
    if (session->row_cells) {
        ghostty_render_state_row_cells_free(session->row_cells);
        session->row_cells = NULL;
    }
    if (session->row_iter) {
        ghostty_render_state_row_iterator_free(session->row_iter);
        session->row_iter = NULL;
    }
    if (session->render_state) {
        ghostty_render_state_free(session->render_state);
        session->render_state = NULL;
    }
    if (session->terminal) {
        ghostty_terminal_free(session->terminal);
        session->terminal = NULL;
    }

    session->launched = false;
    session->visible = true;
    session->prev_focused = true;
    session->scrollbar_dragging = false;
    session->child_exited = false;
    session->child_reaped = false;
    session->child_exit_status = -1;
    session->font_size = 0;
    session->pad = 0;
    session->cell_width = 0;
    session->cell_height = 0;
    session->scr_w = 0;
    session->scr_h = 0;
    session->dpi_scale_x = 1.0f;
    session->dpi_scale_y = 1.0f;
    session->term_cols = 0;
    session->term_rows = 0;
    session->child = -1;
    session->last_input_ms = 0;
    session->last_output_ms = 0;
    session->total_input_bytes = 0;
    session->total_output_bytes = 0;
    session->capture_output = false;
    session->scripted_mouse_buttons = 0;
    session->effects_ctx = (EffectsContext){0};

    boo_sdl_release(session);
}

static void boo_close_fonts(BooSession *session)
{
    if (!session)
        return;

    if (session->mono_font) {
        TTF_CloseFont(session->mono_font);
        session->mono_font = NULL;
    }
    if (session->mono_font_bold) {
        TTF_CloseFont(session->mono_font_bold);
        session->mono_font_bold = NULL;
    }
    if (session->mono_font_italic) {
        TTF_CloseFont(session->mono_font_italic);
        session->mono_font_italic = NULL;
    }
    if (session->mono_font_bold_italic) {
        TTF_CloseFont(session->mono_font_bold_italic);
        session->mono_font_bold_italic = NULL;
    }
}

typedef struct {
    const char *name;
    const char *regular;
    const char *bold;
    const char *italic;
    const char *bold_italic;
} BooFontFamily;

static char *boo_strdup(const char *value)
{
    if (!value)
        return NULL;

    size_t len = strlen(value);
    char *copy = malloc(len + 1);
    if (!copy)
        return NULL;
    memcpy(copy, value, len + 1);
    return copy;
}

static bool boo_file_readable(const char *path)
{
    return path && path[0] != '\0' && access(path, R_OK) == 0;
}

static int boo_store_font_paths(BooSession *session,
                                      const BooFontFamily *family)
{
    session->font_regular_path = boo_strdup(family->regular);
    session->font_bold_path = boo_strdup(family->bold);
    session->font_italic_path = boo_strdup(family->italic);
    session->font_bold_italic_path = boo_strdup(family->bold_italic);
    if (!session->font_regular_path || !session->font_bold_path
        || !session->font_italic_path || !session->font_bold_italic_path) {
        boo_set_errorf(session, "out of memory storing font paths");
        return -1;
    }
    return 0;
}

static int boo_resolve_runtime_fonts(BooSession *session)
{
    static const BooFontFamily families[] = {
        {
            .name = "Hack",
            .regular = BOO_PROJECT_DIR "/fonts/Hack-Regular.ttf",
            .bold = BOO_PROJECT_DIR "/fonts/Hack-Bold.ttf",
            .italic = BOO_PROJECT_DIR "/fonts/Hack-Italic.ttf",
            .bold_italic = BOO_PROJECT_DIR "/fonts/Hack-BoldItalic.ttf",
        },
        {
            .name = "Hack",
            .regular = "/usr/share/fonts/truetype/hack/Hack-Regular.ttf",
            .bold = "/usr/share/fonts/truetype/hack/Hack-Bold.ttf",
            .italic = "/usr/share/fonts/truetype/hack/Hack-Italic.ttf",
            .bold_italic = "/usr/share/fonts/truetype/hack/Hack-BoldItalic.ttf",
        },
        {
            .name = "Liberation Mono",
            .regular = "/usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf",
            .bold = "/usr/share/fonts/truetype/liberation/LiberationMono-Bold.ttf",
            .italic = "/usr/share/fonts/truetype/liberation/LiberationMono-Italic.ttf",
            .bold_italic = "/usr/share/fonts/truetype/liberation/LiberationMono-BoldItalic.ttf",
        },
        {
            .name = "DejaVu Sans Mono",
            .regular = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
            .bold = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf",
            .italic = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Oblique.ttf",
            .bold_italic = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-BoldOblique.ttf",
        },
    };

    for (size_t i = 0; i < SDL_arraysize(families); i++) {
        const BooFontFamily *family = &families[i];
        if (!boo_file_readable(family->regular)
            || !boo_file_readable(family->bold)
            || !boo_file_readable(family->italic)
            || !boo_file_readable(family->bold_italic)) {
            continue;
        }
        if (boo_store_font_paths(session, family) != 0)
            return -1;
        return 0;
    }

    boo_set_errorf(
        session,
        "could not find a usable runtime monospace font family (tried Hack, Liberation Mono, DejaVu Sans Mono)");
    return -1;
}

static TTF_Font *boo_open_font_file(BooSession *session,
                                          const char *path,
                                          float font_size_px)
{
    TTF_Font *font = TTF_OpenFont(path, font_size_px);
    if (!font) {
        boo_set_errorf(session, "TTF_OpenFont failed for %s: %s",
                             path, SDL_GetError());
        return NULL;
    }

    TTF_SetFontKerning(font, false);
    return font;
}

static int boo_session_refresh_metrics(BooSession *session,
                                             bool recreate_font)
{
    if (!session || !session->window || !session->renderer)
        return -1;

    int win_w = 0;
    int win_h = 0;
    int out_w = 0;
    int out_h = 0;
    SDL_GetWindowSize(session->window, &win_w, &win_h);
    if (!SDL_GetCurrentRenderOutputSize(session->renderer, &out_w, &out_h)) {
        boo_set_errorf(session, "SDL_GetCurrentRenderOutputSize failed: %s",
                             SDL_GetError());
        return -1;
    }

    session->dpi_scale_x = (win_w > 0 && out_w > 0)
        ? ((float)out_w / (float)win_w)
        : 1.0f;
    session->dpi_scale_y = (win_h > 0 && out_h > 0)
        ? ((float)out_h / (float)win_h)
        : 1.0f;
    if (session->dpi_scale_x <= 0.0f)
        session->dpi_scale_x = 1.0f;
    if (session->dpi_scale_y <= 0.0f)
        session->dpi_scale_y = 1.0f;

    if (recreate_font || !session->mono_font || !session->mono_font_bold
        || !session->mono_font_italic || !session->mono_font_bold_italic) {
        boo_close_fonts(session);
        float font_size_px = (float)session->font_size * session->dpi_scale_y;
        session->mono_font = boo_open_font_file(
            session, session->font_regular_path, font_size_px);
        session->mono_font_bold = boo_open_font_file(
            session, session->font_bold_path, font_size_px);
        session->mono_font_italic = boo_open_font_file(
            session, session->font_italic_path, font_size_px);
        session->mono_font_bold_italic = boo_open_font_file(
            session, session->font_bold_italic_path, font_size_px);
        if (!session->mono_font || !session->mono_font_bold
            || !session->mono_font_italic || !session->mono_font_bold_italic) {
            boo_close_fonts(session);
            return -1;
        }
    }

    int advance_px = 0;
    if (!TTF_GetGlyphMetrics(session->mono_font, 'M',
                             NULL, NULL, NULL, NULL, &advance_px)
        || advance_px <= 0) {
        boo_set_errorf(session, "TTF_GetGlyphMetrics failed: %s", SDL_GetError());
        return -1;
    }

    int line_height_px = TTF_GetFontLineSkip(session->mono_font);
    if (line_height_px <= 0)
        line_height_px = TTF_GetFontHeight(session->mono_font);

    session->cell_width = (int)((float)advance_px / session->dpi_scale_x + 0.5f);
    session->cell_height = (int)((float)line_height_px / session->dpi_scale_y + 0.5f);
    if (session->cell_width < 1)
        session->cell_width = 1;
    if (session->cell_height < 1)
        session->cell_height = 1;

    return 0;
}

static void boo_session_render_frame(BooSession *session)
{
    if (!session || !session->renderer || !session->terminal || !session->render_state)
        return;

    ghostty_render_state_update(session->render_state, session->terminal);

    GhosttyRenderStateColors bg_colors =
        GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
    ghostty_render_state_colors_get(session->render_state, &bg_colors);

    GhosttyTerminalScrollbar scrollbar = {0};
    GhosttyTerminalScrollbar *scrollbar_ptr = NULL;
    if (ghostty_terminal_get(session->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR,
                             &scrollbar) == GHOSTTY_SUCCESS)
        scrollbar_ptr = &scrollbar;

    SDL_SetRenderDrawColor(session->renderer,
                           bg_colors.background.r,
                           bg_colors.background.g,
                           bg_colors.background.b,
                           255);
    SDL_RenderClear(session->renderer);

    render_terminal(session->renderer,
                    session->mono_font,
                    session->mono_font_bold,
                    session->mono_font_italic,
                    session->mono_font_bold_italic,
                    session->render_state,
                    session->row_iter, session->row_cells,
                    session->cell_width, session->cell_height,
                    session->pad, scrollbar_ptr,
                    session->scr_w, session->scr_h,
                    session->dpi_scale_x, session->dpi_scale_y);

    if (session->child_exited) {
        char exit_msg[128];
        if (session->child_exit_status >= 0)
            snprintf(exit_msg, sizeof(exit_msg),
                     "[process exited with status %d]",
                     session->child_exit_status);
        else
            snprintf(exit_msg, sizeof(exit_msg), "[process exited]");

        int msg_w_px = 0;
        int msg_h_px = 0;
        TTF_GetStringSize(session->mono_font, exit_msg, 0, &msg_w_px, &msg_h_px);
        int msg_w = (int)(msg_w_px / session->dpi_scale_x);
        int msg_h = (int)(msg_h_px / session->dpi_scale_y);
        int banner_h = msg_h + 8;

        SDL_SetRenderDrawBlendMode(session->renderer, SDL_BLENDMODE_BLEND);
        SDL_SetRenderDrawColor(session->renderer, 0, 0, 0, 180);
        SDL_FRect banner_rect = {
            0,
            (float)(session->scr_h - banner_h),
            (float)session->scr_w,
            (float)banner_h,
        };
        SDL_RenderFillRect(session->renderer, &banner_rect);
        SDL_SetRenderDrawBlendMode(session->renderer, SDL_BLENDMODE_NONE);

        SDL_Color white = { 255, 255, 255, 255 };
        SDL_Surface *text_surf =
            TTF_RenderText_Blended(session->mono_font, exit_msg, 0, white);
        if (text_surf) {
            SDL_Texture *text_tex =
                SDL_CreateTextureFromSurface(session->renderer, text_surf);
            if (text_tex) {
                SDL_FRect dst = {
                    (float)(session->scr_w - msg_w) / 2,
                    (float)(session->scr_h - banner_h + 4),
                    (float)msg_w,
                    (float)msg_h,
                };
                SDL_RenderTexture(session->renderer, text_tex, NULL, &dst);
                SDL_DestroyTexture(text_tex);
            }
            SDL_DestroySurface(text_surf);
        }
    }

    SDL_RenderPresent(session->renderer);
}

static void boo_session_process_events(BooSession *session,
                                             bool *quit_requested)
{
    char text_buf[64];
    int text_len = 0;
    SDL_KeyboardEvent key_events[16];
    int num_key_events = 0;
    bool mouse_just_pressed = false;
    bool mouse_just_released = false;

    SDL_Event ev;
    while (SDL_PollEvent(&ev)) {
        switch (ev.type) {
        case SDL_EVENT_QUIT:
        case SDL_EVENT_WINDOW_CLOSE_REQUESTED:
            *quit_requested = true;
            break;

        case SDL_EVENT_KEY_DOWN:
        case SDL_EVENT_KEY_UP:
            if (num_key_events < 16)
                key_events[num_key_events++] = ev.key;
            break;

        case SDL_EVENT_TEXT_INPUT:
            {
                int tlen = (int)strlen(ev.text.text);
                if (text_len + tlen < (int)sizeof(text_buf)) {
                    memcpy(&text_buf[text_len], ev.text.text, (size_t)tlen);
                    text_len += tlen;
                }
            }
            break;

        case SDL_EVENT_MOUSE_BUTTON_DOWN:
            mouse_just_pressed = true;
            if (!session->child_exited) {
                ghostty_mouse_encoder_setopt_from_terminal(session->mouse_encoder,
                                                           session->terminal);

                GhosttyMods mods = sdl_mod_to_ghostty(SDL_GetModState());
                ghostty_mouse_event_set_mods(session->mouse_event, mods);
                ghostty_mouse_event_set_position(
                    session->mouse_event,
                    (GhosttyMousePosition){ .x = ev.button.x, .y = ev.button.y });

                GhosttyMouseButton gbtn =
                    sdl_mouse_to_ghostty(ev.button.button);
                if (gbtn != GHOSTTY_MOUSE_BUTTON_UNKNOWN) {
                    ghostty_mouse_event_set_action(
                        session->mouse_event, GHOSTTY_MOUSE_ACTION_PRESS);
                    ghostty_mouse_event_set_button(session->mouse_event, gbtn);

                    GhosttyMouseEncoderSize enc_size = {
                        .size = sizeof(GhosttyMouseEncoderSize),
                        .screen_width = (uint32_t)session->scr_w,
                        .screen_height = (uint32_t)session->scr_h,
                        .cell_width = (uint32_t)session->cell_width,
                        .cell_height = (uint32_t)session->cell_height,
                        .padding_top = (uint32_t)session->pad,
                        .padding_bottom = (uint32_t)session->pad,
                        .padding_left = (uint32_t)session->pad,
                        .padding_right = (uint32_t)session->pad,
                    };
                    ghostty_mouse_encoder_setopt(session->mouse_encoder,
                        GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &enc_size);

                    bool any_pressed =
                        (SDL_GetMouseState(NULL, NULL) & SDL_BUTTON_LMASK) != 0;
                    ghostty_mouse_encoder_setopt(session->mouse_encoder,
                        GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED,
                        &any_pressed);

                    mouse_encode_and_write(session, session->mouse_encoder,
                                           session->mouse_event);
                }
            }
            break;

        case SDL_EVENT_MOUSE_BUTTON_UP:
            mouse_just_released = true;
            if (!session->child_exited) {
                ghostty_mouse_encoder_setopt_from_terminal(session->mouse_encoder,
                                                           session->terminal);

                GhosttyMods mods = sdl_mod_to_ghostty(SDL_GetModState());
                ghostty_mouse_event_set_mods(session->mouse_event, mods);
                ghostty_mouse_event_set_position(
                    session->mouse_event,
                    (GhosttyMousePosition){ .x = ev.button.x, .y = ev.button.y });

                GhosttyMouseButton gbtn =
                    sdl_mouse_to_ghostty(ev.button.button);
                if (gbtn != GHOSTTY_MOUSE_BUTTON_UNKNOWN) {
                    ghostty_mouse_event_set_action(
                        session->mouse_event, GHOSTTY_MOUSE_ACTION_RELEASE);
                    ghostty_mouse_event_set_button(session->mouse_event, gbtn);

                    GhosttyMouseEncoderSize enc_size = {
                        .size = sizeof(GhosttyMouseEncoderSize),
                        .screen_width = (uint32_t)session->scr_w,
                        .screen_height = (uint32_t)session->scr_h,
                        .cell_width = (uint32_t)session->cell_width,
                        .cell_height = (uint32_t)session->cell_height,
                        .padding_top = (uint32_t)session->pad,
                        .padding_bottom = (uint32_t)session->pad,
                        .padding_left = (uint32_t)session->pad,
                        .padding_right = (uint32_t)session->pad,
                    };
                    ghostty_mouse_encoder_setopt(session->mouse_encoder,
                        GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &enc_size);

                    bool any_pressed = false;
                    ghostty_mouse_encoder_setopt(session->mouse_encoder,
                        GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED,
                        &any_pressed);

                    mouse_encode_and_write(session, session->mouse_encoder,
                                           session->mouse_event);
                }
            }
            break;

        case SDL_EVENT_MOUSE_MOTION:
            if (!session->child_exited) {
                ghostty_mouse_encoder_setopt_from_terminal(session->mouse_encoder,
                                                           session->terminal);

                GhosttyMods mods = sdl_mod_to_ghostty(SDL_GetModState());
                ghostty_mouse_event_set_mods(session->mouse_event, mods);
                ghostty_mouse_event_set_position(
                    session->mouse_event,
                    (GhosttyMousePosition){ .x = ev.motion.x, .y = ev.motion.y });
                ghostty_mouse_event_set_action(session->mouse_event,
                    GHOSTTY_MOUSE_ACTION_MOTION);

                if (ev.motion.state & SDL_BUTTON_LMASK)
                    ghostty_mouse_event_set_button(
                        session->mouse_event, GHOSTTY_MOUSE_BUTTON_LEFT);
                else if (ev.motion.state & SDL_BUTTON_RMASK)
                    ghostty_mouse_event_set_button(
                        session->mouse_event, GHOSTTY_MOUSE_BUTTON_RIGHT);
                else if (ev.motion.state & SDL_BUTTON_MMASK)
                    ghostty_mouse_event_set_button(
                        session->mouse_event, GHOSTTY_MOUSE_BUTTON_MIDDLE);
                else
                    ghostty_mouse_event_clear_button(session->mouse_event);

                GhosttyMouseEncoderSize enc_size = {
                    .size = sizeof(GhosttyMouseEncoderSize),
                    .screen_width = (uint32_t)session->scr_w,
                    .screen_height = (uint32_t)session->scr_h,
                    .cell_width = (uint32_t)session->cell_width,
                    .cell_height = (uint32_t)session->cell_height,
                    .padding_top = (uint32_t)session->pad,
                    .padding_bottom = (uint32_t)session->pad,
                    .padding_left = (uint32_t)session->pad,
                    .padding_right = (uint32_t)session->pad,
                };
                ghostty_mouse_encoder_setopt(session->mouse_encoder,
                    GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &enc_size);

                bool any_pressed = (ev.motion.state != 0);
                ghostty_mouse_encoder_setopt(session->mouse_encoder,
                    GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, &any_pressed);

                bool track_cell = true;
                ghostty_mouse_encoder_setopt(session->mouse_encoder,
                    GHOSTTY_MOUSE_ENCODER_OPT_TRACK_LAST_CELL, &track_cell);

                mouse_encode_and_write(session, session->mouse_encoder,
                                       session->mouse_event);
            }
            break;

        case SDL_EVENT_MOUSE_WHEEL:
            if (!session->child_exited) {
                bool mouse_tracking = false;
                ghostty_terminal_get(session->terminal,
                    GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &mouse_tracking);

                if (mouse_tracking) {
                    ghostty_mouse_encoder_setopt_from_terminal(session->mouse_encoder,
                                                               session->terminal);

                    float mx;
                    float my;
                    SDL_GetMouseState(&mx, &my);
                    ghostty_mouse_event_set_position(
                        session->mouse_event,
                        (GhosttyMousePosition){ .x = mx, .y = my });

                    GhosttyMods mods = sdl_mod_to_ghostty(SDL_GetModState());
                    ghostty_mouse_event_set_mods(session->mouse_event, mods);

                    GhosttyMouseEncoderSize enc_size = {
                        .size = sizeof(GhosttyMouseEncoderSize),
                        .screen_width = (uint32_t)session->scr_w,
                        .screen_height = (uint32_t)session->scr_h,
                        .cell_width = (uint32_t)session->cell_width,
                        .cell_height = (uint32_t)session->cell_height,
                        .padding_top = (uint32_t)session->pad,
                        .padding_bottom = (uint32_t)session->pad,
                        .padding_left = (uint32_t)session->pad,
                        .padding_right = (uint32_t)session->pad,
                    };
                    ghostty_mouse_encoder_setopt(session->mouse_encoder,
                        GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &enc_size);

                    GhosttyMouseButton scroll_btn = (ev.wheel.y > 0)
                        ? GHOSTTY_MOUSE_BUTTON_FOUR
                        : GHOSTTY_MOUSE_BUTTON_FIVE;
                    ghostty_mouse_event_set_button(session->mouse_event, scroll_btn);
                    ghostty_mouse_event_set_action(session->mouse_event,
                        GHOSTTY_MOUSE_ACTION_PRESS);
                    mouse_encode_and_write(session, session->mouse_encoder,
                                           session->mouse_event);
                    ghostty_mouse_event_set_action(session->mouse_event,
                        GHOSTTY_MOUSE_ACTION_RELEASE);
                    mouse_encode_and_write(session, session->mouse_encoder,
                                           session->mouse_event);
                } else {
                    int delta = (ev.wheel.y > 0) ? -3 : 3;
                    GhosttyTerminalScrollViewport sv = {
                        .tag = GHOSTTY_SCROLL_VIEWPORT_DELTA,
                        .value = { .delta = delta },
                    };
                    ghostty_terminal_scroll_viewport(session->terminal, sv);
                }
            }
            break;

        case SDL_EVENT_WINDOW_PIXEL_SIZE_CHANGED:
            {
                int w = 0;
                int h = 0;
                SDL_GetWindowSize(session->window, &w, &h);
                session->scr_w = w;
                session->scr_h = h;
                SDL_SetRenderLogicalPresentation(session->renderer, w, h,
                    SDL_LOGICAL_PRESENTATION_STRETCH);
                if (boo_session_refresh_metrics(session, true) != 0)
                    break;
                int cols = (w - 2 * session->pad) / session->cell_width;
                int rows = (h - 2 * session->pad) / session->cell_height;
                if (cols < 1) cols = 1;
                if (rows < 1) rows = 1;
                session->term_cols = (uint16_t)cols;
                session->term_rows = (uint16_t)rows;
                ghostty_terminal_resize(session->terminal,
                                        session->term_cols,
                                        session->term_rows,
                                        (uint32_t)session->cell_width,
                                        (uint32_t)session->cell_height);
                session->effects_ctx.cols = session->term_cols;
                session->effects_ctx.rows = session->term_rows;
                struct winsize new_ws = {
                    .ws_row = session->term_rows,
                    .ws_col = session->term_cols,
                    .ws_xpixel = (unsigned short)(session->term_cols * session->cell_width),
                    .ws_ypixel = (unsigned short)(session->term_rows * session->cell_height),
                };
                ioctl(session->pty_fd, TIOCSWINSZ, &new_ws);
                glyph_cache_clear();
            }
            break;

        case SDL_EVENT_WINDOW_FOCUS_GAINED:
        case SDL_EVENT_WINDOW_FOCUS_LOST:
            {
                bool focused = (ev.type == SDL_EVENT_WINDOW_FOCUS_GAINED);
                if (focused != session->prev_focused) {
                    bool focus_mode = false;
                    if (!session->child_exited
                        && ghostty_terminal_mode_get(session->terminal,
                               GHOSTTY_MODE_FOCUS_EVENT, &focus_mode) == GHOSTTY_SUCCESS
                        && focus_mode) {
                        GhosttyFocusEvent focus_event = focused
                            ? GHOSTTY_FOCUS_GAINED
                            : GHOSTTY_FOCUS_LOST;
                        char focus_buf[8];
                        size_t focus_written = 0;
                        GhosttyResult focus_res = ghostty_focus_encode(
                            focus_event, focus_buf, sizeof(focus_buf),
                            &focus_written);
                        if (focus_res == GHOSTTY_SUCCESS && focus_written > 0)
                            boo_session_write_pty(session, focus_buf, focus_written);
                    }
                    session->prev_focused = focused;
                }
            }
            break;
        }
    }

    if (!session->child_exited && num_key_events > 0) {
        ghostty_key_encoder_setopt_from_terminal(session->key_encoder,
                                                 session->terminal);

        bool text_consumed = false;
        for (int i = 0; i < num_key_events; i++) {
            const char *txt = NULL;
            int txt_len = 0;
            if (!text_consumed && text_len > 0
                && key_events[i].type == SDL_EVENT_KEY_DOWN) {
                txt = text_buf;
                txt_len = text_len;
                text_consumed = true;
            }
            handle_key_event(session, session->key_encoder,
                             session->key_event, &key_events[i], txt, txt_len);
        }

        if (!text_consumed && text_len > 0)
            boo_session_write_pty(session, text_buf, (size_t)text_len);
    }

    {
        float fmx = 0.0f;
        float fmy = 0.0f;
        SDL_MouseButtonFlags buttons = SDL_GetMouseState(&fmx, &fmy);
        int mx = (int)fmx;
        int my = (int)fmy;
        bool held = (buttons & SDL_BUTTON_LMASK) != 0;
        handle_scrollbar(session->terminal, session->render_state,
                         &session->scrollbar_dragging,
                         session->scr_w, session->scr_h,
                         mx, my,
                         mouse_just_pressed, held, mouse_just_released);
    }
}

// write_pty effect — the terminal calls this whenever a VT sequence
// requires a response back to the application (device status reports,
// mode queries, device attributes, etc.).  Without this, programs like
// vim and tmux that probe terminal capabilities would hang.
static void effect_write_pty(GhosttyTerminal terminal, void *userdata,
                             const uint8_t *data, size_t len)
{
    (void)terminal;
    EffectsContext *ctx = (EffectsContext *)userdata;
    boo_session_write_pty(ctx->session, data, len);
}

// size effect — responds to XTWINOPS size queries (CSI 14/16/18 t)
// so programs can discover the terminal geometry in cells and pixels.
static bool effect_size(GhosttyTerminal terminal, void *userdata,
                        GhosttySizeReportSize *out_size)
{
    (void)terminal;
    EffectsContext *ctx = (EffectsContext *)userdata;
    out_size->rows = ctx->rows;
    out_size->columns = ctx->cols;
    out_size->cell_width = (uint32_t)ctx->cell_width;
    out_size->cell_height = (uint32_t)ctx->cell_height;
    return true;
}

// device_attributes effect — responds to DA1/DA2/DA3 queries so
// terminal applications can identify the terminal's capabilities.
// We report VT220-level conformance with a modest feature set.
static bool effect_device_attributes(GhosttyTerminal terminal, void *userdata,
                                     GhosttyDeviceAttributes *out_attrs)
{
    (void)terminal;
    (void)userdata;

    // DA1: VT220-level with a few common features.
    out_attrs->primary.conformance_level = GHOSTTY_DA_CONFORMANCE_VT220;
    out_attrs->primary.features[0] = GHOSTTY_DA_FEATURE_COLUMNS_132;
    out_attrs->primary.features[1] = GHOSTTY_DA_FEATURE_SELECTIVE_ERASE;
    out_attrs->primary.features[2] = GHOSTTY_DA_FEATURE_ANSI_COLOR;
    out_attrs->primary.num_features = 3;

    // DA2: VT220-type, version 1, no ROM cartridge.
    out_attrs->secondary.device_type = GHOSTTY_DA_DEVICE_TYPE_VT220;
    out_attrs->secondary.firmware_version = 1;
    out_attrs->secondary.rom_cartridge = 0;

    // DA3: arbitrary unit id.
    out_attrs->tertiary.unit_id = 0;

    return true;
}

// xtversion effect — responds to CSI > q with our application name.
static GhosttyString effect_xtversion(GhosttyTerminal terminal, void *userdata)
{
    (void)terminal;
    (void)userdata;
    return (GhosttyString){ .ptr = (const uint8_t *)"boo", .len = 9 };
}

// title_changed effect — updates the SDL window title whenever the
// terminal receives an OSC 0 or OSC 2 title-setting sequence.
static void effect_title_changed(GhosttyTerminal terminal, void *userdata)
{
    EffectsContext *ctx = (EffectsContext *)userdata;
    GhosttyString title = {0};
    if (ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_TITLE, &title) != GHOSTTY_SUCCESS)
        return;

    // SDL_SetWindowTitle expects a NUL-terminated string, so copy
    // into a stack buffer.  Truncate quietly if the title is absurdly long.
    char buf[256];
    size_t len = title.len < sizeof(buf) - 1 ? title.len : sizeof(buf) - 1;
    memcpy(buf, title.ptr, len);
    buf[len] = '\0';
    SDL_SetWindowTitle(ctx->window, buf);
}

// color_scheme effect — responds to CSI ? 996 n.  We return false to
// silently ignore the query rather than guessing.
static bool effect_color_scheme(GhosttyTerminal terminal, void *userdata,
                                GhosttyColorScheme *out_scheme)
{
    (void)terminal;
    (void)userdata;
    (void)out_scheme;
    return false;
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

BooSession *boo_session_new(void)
{
    BooSession *session = calloc(1, sizeof(*session));
    if (!session)
        return NULL;

    session->pty_fd = -1;
    session->child = -1;
    session->child_exit_status = -1;
    session->dpi_scale_x = 1.0f;
    session->dpi_scale_y = 1.0f;
    session->visible = true;
    session->prev_focused = true;
    return session;
}

void boo_session_free(BooSession *session)
{
    if (!session)
        return;

    boo_session_cleanup(session);
    free(session);
}

const char *boo_session_last_error(const BooSession *session)
{
    if (!session || session->last_error[0] == '\0')
        return "";
    return session->last_error;
}

int boo_session_launch(BooSession *session,
                             const char *const *argv,
                             const BooLaunchOptions *options)
{
    if (!session)
        return -1;

    boo_clear_error(session);
    boo_session_cleanup(session);

    BooLaunchOptions cfg = {
        .size = sizeof(cfg),
        .cols = 80,
        .rows = 24,
        .font_size = 16,
        .padding = 4,
        .cwd = NULL,
        .env = NULL,
        .visible = true,
        .window_title = "boo",
    };
    if (options) {
        size_t copy_size = options->size;
        if (copy_size == 0 || copy_size > sizeof(cfg))
            copy_size = sizeof(cfg);
        memcpy(&cfg, options, copy_size);
    }

    if (cfg.cols == 0)
        cfg.cols = 80;
    if (cfg.rows == 0)
        cfg.rows = 24;
    if (cfg.font_size <= 0)
        cfg.font_size = 16;
    if (cfg.padding < 0)
        cfg.padding = 4;
    if (!cfg.window_title)
        cfg.window_title = "boo";

    if (boo_sdl_acquire(session) != 0)
        return -1;

    if (boo_resolve_runtime_fonts(session) != 0) {
        boo_session_cleanup(session);
        return -1;
    }

    session->font_size = cfg.font_size;
    session->pad = cfg.padding;
    session->visible = cfg.visible;
    session->term_cols = cfg.cols;
    session->term_rows = cfg.rows;
    int provisional_cell_w = session->font_size > 0 ? session->font_size : 16;
    int provisional_cell_h = provisional_cell_w + 4;
    session->scr_w = session->term_cols * provisional_cell_w + 2 * session->pad;
    session->scr_h = session->term_rows * provisional_cell_h + 2 * session->pad;

    Uint64 window_flags = SDL_WINDOW_RESIZABLE | SDL_WINDOW_HIGH_PIXEL_DENSITY;
    if (!cfg.visible)
        window_flags |= SDL_WINDOW_HIDDEN;

    session->window = SDL_CreateWindow(cfg.window_title,
                                       session->scr_w,
                                       session->scr_h,
                                       window_flags);
    if (!session->window) {
        boo_set_errorf(session, "SDL_CreateWindow failed: %s", SDL_GetError());
        boo_session_cleanup(session);
        return -1;
    }

    session->renderer = SDL_CreateRenderer(session->window, NULL);
    if (!session->renderer) {
        boo_set_errorf(session, "SDL_CreateRenderer failed: %s", SDL_GetError());
        boo_session_cleanup(session);
        return -1;
    }

    if (boo_session_refresh_metrics(session, true) != 0) {
        boo_session_cleanup(session);
        return -1;
    }

    session->scr_w = session->term_cols * session->cell_width + 2 * session->pad;
    session->scr_h = session->term_rows * session->cell_height + 2 * session->pad;
    SDL_SetWindowSize(session->window, session->scr_w, session->scr_h);
    SDL_SetRenderLogicalPresentation(session->renderer,
                                     session->scr_w,
                                     session->scr_h,
                                     SDL_LOGICAL_PRESENTATION_STRETCH);
    SDL_SetRenderVSync(session->renderer, 0);

    SDL_StartTextInput(session->window);
    session->text_input_started = true;

    GhosttyTerminalOptions terminal_opts = {
        .cols = session->term_cols,
        .rows = session->term_rows,
        .max_scrollback = 1000,
    };
    GhosttyResult err = ghostty_terminal_new(NULL, &session->terminal, terminal_opts);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_terminal_new failed (%d)", err);
        boo_session_cleanup(session);
        return -1;
    }

    session->pty_fd = pty_spawn(&session->child, session->term_cols, session->term_rows,
                                session->cell_width, session->cell_height,
                                argv, cfg.cwd, cfg.env);
    if (session->pty_fd < 0) {
        boo_set_errorf(session, "pty_spawn failed: %s", strerror(errno));
        boo_session_cleanup(session);
        return -1;
    }

    session->effects_ctx = (EffectsContext){
        .session = session,
        .pty_fd = session->pty_fd,
        .cell_width = session->cell_width,
        .cell_height = session->cell_height,
        .cols = session->term_cols,
        .rows = session->term_rows,
        .window = session->window,
    };
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_USERDATA,
                         &session->effects_ctx);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_WRITE_PTY,
                         (const void *)effect_write_pty);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_SIZE,
                         (const void *)effect_size);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_DEVICE_ATTRIBUTES,
                         (const void *)effect_device_attributes);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_XTVERSION,
                         (const void *)effect_xtversion);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_TITLE_CHANGED,
                         (const void *)effect_title_changed);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_COLOR_SCHEME,
                         (const void *)effect_color_scheme);

    err = ghostty_key_encoder_new(NULL, &session->key_encoder);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_key_encoder_new failed (%d)", err);
        boo_session_cleanup(session);
        return -1;
    }
    err = ghostty_key_event_new(NULL, &session->key_event);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_key_event_new failed (%d)", err);
        boo_session_cleanup(session);
        return -1;
    }
    err = ghostty_mouse_encoder_new(NULL, &session->mouse_encoder);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_mouse_encoder_new failed (%d)", err);
        boo_session_cleanup(session);
        return -1;
    }
    err = ghostty_mouse_event_new(NULL, &session->mouse_event);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_mouse_event_new failed (%d)", err);
        boo_session_cleanup(session);
        return -1;
    }
    err = ghostty_render_state_new(NULL, &session->render_state);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_render_state_new failed (%d)", err);
        boo_session_cleanup(session);
        return -1;
    }
    err = ghostty_render_state_row_iterator_new(NULL, &session->row_iter);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session,
                             "ghostty_render_state_row_iterator_new failed (%d)",
                             err);
        boo_session_cleanup(session);
        return -1;
    }
    err = ghostty_render_state_row_cells_new(NULL, &session->row_cells);
    if (err != GHOSTTY_SUCCESS) {
        boo_set_errorf(session,
                             "ghostty_render_state_row_cells_new failed (%d)",
                             err);
        boo_session_cleanup(session);
        return -1;
    }

    session->launched = true;
    session->prev_focused = true;
    session->scrollbar_dragging = false;
    session->child_exited = false;
    session->child_reaped = false;
    session->child_exit_status = -1;
    session->last_input_ms = boo_now_ms();
    session->last_output_ms = session->last_input_ms;
    session->capture_output = false;

    boo_session_render_frame(session);
    return 0;
}

int boo_session_step(BooSession *session, int timeout_ms)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }

    boo_clear_error(session);

    if (timeout_ms < 0)
        timeout_ms = 0;
    Uint64 deadline = SDL_GetTicks() + (Uint64)timeout_ms;

    for (;;) {
        bool quit_requested = false;
        boo_session_process_events(session, &quit_requested);

        if (quit_requested)
            boo_session_terminate(session);

        if (!session->child_exited) {
            PtyReadResult pty_rc = pty_read(session->pty_fd,
                                            session->terminal,
                                            session->capture_output
                                                ? &session->output_bytes
                                                : NULL,
                                            &session->total_output_bytes,
                                            &session->last_output_ms);
            if (pty_rc != PTY_READ_OK)
                session->child_exited = true;
        }

        if (session->child_exited && !session->child_reaped) {
            int wstatus = 0;
            pid_t wp = waitpid(session->child, &wstatus, WNOHANG);
            if (wp > 0) {
                session->child_reaped = true;
                if (WIFEXITED(wstatus))
                    session->child_exit_status = WEXITSTATUS(wstatus);
                else if (WIFSIGNALED(wstatus))
                    session->child_exit_status = 128 + WTERMSIG(wstatus);
            }
        }

        boo_session_render_frame(session);

        if (timeout_ms == 0 || session->child_exited || SDL_GetTicks() >= deadline)
            break;

        SDL_Delay(10);
    }

    return 0;
}

int boo_session_send_bytes(BooSession *session, const void *data, size_t len)
{
    if (!session || !session->launched || (!data && len > 0)) {
        boo_set_errorf(session, "send_bytes requires a launched session and bytes");
        return -1;
    }
    if (session->child_exited) {
        boo_set_errorf(session, "child process has already exited");
        return -1;
    }

    boo_clear_error(session);
    size_t written = boo_session_write_pty(session, data, len);
    return boo_session_require_full_write(session, written, len, "send_bytes");
}

int boo_session_send_text(BooSession *session, const char *utf8)
{
    if (!utf8) {
        boo_set_errorf(session, "send_text requires a launched session and text");
        return -1;
    }

    return boo_session_send_bytes(session, utf8, strlen(utf8));
}

static int boo_send_key_with_action(BooSession *session,
                                    BooKey key,
                                    uint16_t modifiers,
                                    GhosttyKeyAction action)
{
    GhosttyKey gkey = boo_key_to_ghostty(key);
    if (gkey == GHOSTTY_KEY_UNIDENTIFIED) {
        boo_set_errorf(session, "unsupported key value %d", (int)key);
        return -1;
    }

    ghostty_key_encoder_setopt_from_terminal(session->key_encoder, session->terminal);

    GhosttyMods mods = boo_mods_to_ghostty(modifiers);
    ghostty_key_event_set_key(session->key_event, gkey);
    ghostty_key_event_set_action(session->key_event, action);
    ghostty_key_event_set_mods(session->key_event, mods);

    uint32_t unshifted = boo_key_to_unshifted_codepoint(key);
    ghostty_key_event_set_unshifted_codepoint(session->key_event, unshifted);

    GhosttyMods consumed = 0;
    char utf8_buf[4];
    size_t utf8_len = 0;
    if (unshifted != 0
        && (mods & (GHOSTTY_MODS_CTRL | GHOSTTY_MODS_ALT | GHOSTTY_MODS_SUPER)) == 0) {
        uint32_t cp = (mods & GHOSTTY_MODS_SHIFT)
            ? boo_key_to_shifted_codepoint(key)
            : unshifted;
        utf8_len = (size_t)utf8_encode(cp, utf8_buf);
        if (mods & GHOSTTY_MODS_SHIFT)
            consumed |= GHOSTTY_MODS_SHIFT;
    }
    ghostty_key_event_set_consumed_mods(session->key_event, consumed);
    if (utf8_len > 0)
        ghostty_key_event_set_utf8(session->key_event, utf8_buf, utf8_len);
    else
        ghostty_key_event_set_utf8(session->key_event, NULL, 0);

    char small_buf[128];
    size_t written = 0;
    GhosttyResult res = ghostty_key_encoder_encode(session->key_encoder,
                                                   session->key_event,
                                                   small_buf,
                                                   sizeof(small_buf),
                                                   &written);
    if (res == GHOSTTY_OUT_OF_SPACE) {
        char *dynamic_buf = malloc(written);
        if (!dynamic_buf) {
            boo_set_errorf(session, "out of memory encoding key event");
            return -1;
        }
        res = ghostty_key_encoder_encode(session->key_encoder,
                                         session->key_event,
                                         dynamic_buf,
                                         written,
                                         &written);
        if (res == GHOSTTY_SUCCESS && written > 0) {
            size_t sent = boo_session_write_pty(session, dynamic_buf, written);
            if (boo_session_require_full_write(session, sent, written, "send_key") != 0) {
                free(dynamic_buf);
                return -1;
            }
        }
        free(dynamic_buf);
    } else if (res == GHOSTTY_SUCCESS && written > 0) {
        size_t sent = boo_session_write_pty(session, small_buf, written);
        if (boo_session_require_full_write(session, sent, written, "send_key") != 0)
            return -1;
    }

    if (res != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_key_encoder_encode failed (%d)", res);
        return -1;
    }

    return 0;
}

int boo_session_send_key(BooSession *session,
                               BooKey key,
                               uint16_t modifiers)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }
    if (session->child_exited) {
        boo_set_errorf(session, "child process has already exited");
        return -1;
    }
    boo_clear_error(session);

    int rc = boo_send_key_with_action(session, key, modifiers, GHOSTTY_KEY_ACTION_PRESS);
    if (rc != 0) return rc;

    return boo_send_key_with_action(session, key, modifiers, GHOSTTY_KEY_ACTION_RELEASE);
}

int boo_session_send_key_action(BooSession *session,
                               BooKey key,
                               uint16_t modifiers,
                               int action)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }
    if (session->child_exited) {
        boo_set_errorf(session, "child process has already exited");
        return -1;
    }
    boo_clear_error(session);

    if (action == BOO_KEY_ACTION_PRESS_AND_RELEASE) {
        int rc = boo_send_key_with_action(session, key, modifiers, GHOSTTY_KEY_ACTION_PRESS);
        if (rc != 0) return rc;
        return boo_send_key_with_action(session, key, modifiers, GHOSTTY_KEY_ACTION_RELEASE);
    }

    GhosttyKeyAction gaction;
    switch (action) {
        case BOO_KEY_ACTION_RELEASE: gaction = GHOSTTY_KEY_ACTION_RELEASE; break;
        case BOO_KEY_ACTION_REPEAT:  gaction = GHOSTTY_KEY_ACTION_REPEAT;  break;
        default:                     gaction = GHOSTTY_KEY_ACTION_PRESS;   break;
    }

    return boo_send_key_with_action(session, key, modifiers, gaction);
}

int boo_session_send_mouse_button(BooSession *session,
                                  int x,
                                  int y,
                                  BooMouseButton button,
                                  uint16_t modifiers,
                                  bool pressed)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }
    if (session->child_exited) {
        boo_set_errorf(session, "child process has already exited");
        return -1;
    }

    boo_clear_error(session);

    GhosttyMouseButton gbutton = boo_mouse_button_to_ghostty(button);
    if (gbutton == GHOSTTY_MOUSE_BUTTON_UNKNOWN) {
        boo_set_errorf(session, "unsupported mouse button value %d", (int)button);
        return -1;
    }

    GhosttyMousePosition position = {0};
    if (boo_session_mouse_position(session, x, y, &position) != 0)
        return -1;

    uint16_t mask = boo_mouse_button_to_mask(button);
    if (pressed)
        session->scripted_mouse_buttons |= mask;
    else
        session->scripted_mouse_buttons &= (uint16_t)~mask;

    GhosttyMods mods = boo_mods_to_ghostty(modifiers);
    boo_session_configure_mouse_encoder(session, position, mods);
    ghostty_mouse_event_set_action(session->mouse_event,
        pressed ? GHOSTTY_MOUSE_ACTION_PRESS : GHOSTTY_MOUSE_ACTION_RELEASE);
    ghostty_mouse_event_set_button(session->mouse_event, gbutton);
    mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
    return 0;
}

int boo_session_send_mouse_move(BooSession *session,
                                int x,
                                int y,
                                uint16_t modifiers)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }
    if (session->child_exited) {
        boo_set_errorf(session, "child process has already exited");
        return -1;
    }

    boo_clear_error(session);

    GhosttyMousePosition position = {0};
    if (boo_session_mouse_position(session, x, y, &position) != 0)
        return -1;

    GhosttyMods mods = boo_mods_to_ghostty(modifiers);
    boo_session_configure_mouse_encoder(session, position, mods);
    ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_MOTION);
    GhosttyMouseButton held_button = boo_mouse_primary_button(session->scripted_mouse_buttons);
    if (held_button == GHOSTTY_MOUSE_BUTTON_UNKNOWN)
        ghostty_mouse_event_clear_button(session->mouse_event);
    else
        ghostty_mouse_event_set_button(session->mouse_event, held_button);
    mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
    return 0;
}

int boo_session_send_mouse_wheel(BooSession *session,
                                 int x,
                                 int y,
                                 int delta_x,
                                 int delta_y,
                                 uint16_t modifiers)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }
    if (session->child_exited) {
        boo_set_errorf(session, "child process has already exited");
        return -1;
    }

    boo_clear_error(session);

    GhosttyMousePosition position = {0};
    if (boo_session_mouse_position(session, x, y, &position) != 0)
        return -1;

    if (delta_x == 0 && delta_y == 0)
        return 0;

    bool mouse_tracking = false;
    ghostty_terminal_get(session->terminal,
        GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &mouse_tracking);
    if (!mouse_tracking) {
        GhosttyTerminalScrollViewport sv = {
            .tag = GHOSTTY_SCROLL_VIEWPORT_DELTA,
            .value = { .delta = delta_y != 0 ? -delta_y * 3 : delta_x * 3 },
        };
        ghostty_terminal_scroll_viewport(session->terminal, sv);
        return 0;
    }

    GhosttyMods mods = boo_mods_to_ghostty(modifiers);
    boo_session_configure_mouse_encoder(session, position, mods);

    for (int remaining = delta_y; remaining != 0; ) {
        GhosttyMouseButton button = remaining > 0
            ? GHOSTTY_MOUSE_BUTTON_FOUR
            : GHOSTTY_MOUSE_BUTTON_FIVE;
        ghostty_mouse_event_set_button(session->mouse_event, button);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_PRESS);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_RELEASE);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        remaining += remaining > 0 ? -1 : 1;
    }

    for (int remaining = delta_x; remaining != 0; ) {
        GhosttyMouseButton button = remaining > 0
            ? GHOSTTY_MOUSE_BUTTON_SIX
            : GHOSTTY_MOUSE_BUTTON_SEVEN;
        ghostty_mouse_event_set_button(session->mouse_event, button);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_PRESS);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_RELEASE);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        remaining += remaining > 0 ? -1 : 1;
    }

    return 0;
}

char *boo_session_snapshot_text(BooSession *session,
                                      bool trim,
                                      bool unwrap)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return NULL;
    }

    boo_clear_error(session);

    GhosttyFormatterTerminalOptions opts =
        GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalOptions);
    opts.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
    opts.trim = trim;
    opts.unwrap = unwrap;

    GhosttyFormatter formatter = NULL;
    GhosttyResult res = ghostty_formatter_terminal_new(NULL, &formatter,
                                                       session->terminal, opts);
    if (res != GHOSTTY_SUCCESS) {
        boo_set_errorf(session,
                             "ghostty_formatter_terminal_new failed (%d)", res);
        return NULL;
    }

    size_t needed = 0;
    res = ghostty_formatter_format_buf(formatter, NULL, 0, &needed);
    if (res != GHOSTTY_OUT_OF_SPACE && res != GHOSTTY_SUCCESS) {
        ghostty_formatter_free(formatter);
        boo_set_errorf(session, "ghostty_formatter_format_buf failed (%d)", res);
        return NULL;
    }

    char *text = malloc(needed + 1);
    if (!text) {
        ghostty_formatter_free(formatter);
        boo_set_errorf(session, "out of memory creating snapshot");
        return NULL;
    }

    size_t written = 0;
    res = ghostty_formatter_format_buf(formatter, (uint8_t *)text, needed, &written);
    ghostty_formatter_free(formatter);
    if (res != GHOSTTY_SUCCESS) {
        free(text);
        boo_set_errorf(session, "ghostty_formatter_format_buf failed (%d)", res);
        return NULL;
    }

    text[written] = '\0';
    return text;
}

char *boo_session_snapshot_input(BooSession *session, size_t *len_out)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return NULL;
    }

    boo_clear_error(session);
    char *snapshot = boo_byte_buffer_snapshot(&session->input_bytes, len_out);
    if (!snapshot)
        boo_set_errorf(session, "out of memory creating input snapshot");
    return snapshot;
}

char *boo_session_snapshot_output(BooSession *session, size_t *len_out)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return NULL;
    }

    boo_clear_error(session);
    char *snapshot = boo_byte_buffer_snapshot(&session->output_bytes, len_out);
    if (!snapshot)
        boo_set_errorf(session, "out of memory creating output snapshot");
    return snapshot;
}

int boo_session_snapshot_activity(BooSession *session,
                                  BooActivitySnapshot *out_snapshot)
{
    if (!session || !session->launched || !out_snapshot) {
        boo_set_errorf(session, "activity snapshot requires a launched session");
        return -1;
    }

    boo_clear_error(session);

    BooActivitySnapshot snapshot = {
        .size = sizeof(snapshot),
        .input_bytes = session->total_input_bytes,
        .output_bytes = session->total_output_bytes,
        .input_quiet_ms = 0,
        .output_quiet_ms = 0,
    };

    uint64_t now_ms = boo_now_ms();
    if (session->last_input_ms <= now_ms)
        snapshot.input_quiet_ms = now_ms - session->last_input_ms;
    if (session->last_output_ms <= now_ms)
        snapshot.output_quiet_ms = now_ms - session->last_output_ms;

    size_t copy_size = out_snapshot->size;
    if (copy_size == 0 || copy_size > sizeof(snapshot))
        copy_size = sizeof(snapshot);
    memcpy(out_snapshot, &snapshot, copy_size);
    return 0;
}

int boo_session_set_output_capture(BooSession *session, bool enabled)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }

    boo_clear_error(session);
    session->capture_output = enabled;
    return 0;
}

void boo_buffer_free(void *data)
{
    free(data);
}

void boo_string_free(char *text)
{
    free(text);
}

int boo_session_resize(BooSession *session,
                             uint16_t cols,
                             uint16_t rows)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }
    if (cols == 0 || rows == 0) {
        boo_set_errorf(session, "resize requires non-zero cols and rows");
        return -1;
    }

    boo_clear_error(session);

    session->term_cols = cols;
    session->term_rows = rows;
    session->scr_w = session->term_cols * session->cell_width + 2 * session->pad;
    session->scr_h = session->term_rows * session->cell_height + 2 * session->pad;

    if (session->window)
        SDL_SetWindowSize(session->window, session->scr_w, session->scr_h);
    if (session->renderer) {
        if (boo_session_refresh_metrics(session, true) != 0)
            return -1;
        session->scr_w = session->term_cols * session->cell_width + 2 * session->pad;
        session->scr_h = session->term_rows * session->cell_height + 2 * session->pad;
        if (session->window)
            SDL_SetWindowSize(session->window, session->scr_w, session->scr_h);
        SDL_SetRenderLogicalPresentation(session->renderer,
                                         session->scr_w,
                                         session->scr_h,
                                         SDL_LOGICAL_PRESENTATION_STRETCH);
    }

    ghostty_terminal_resize(session->terminal,
                            session->term_cols,
                            session->term_rows,
                            (uint32_t)session->cell_width,
                            (uint32_t)session->cell_height);
    session->effects_ctx.cols = session->term_cols;
    session->effects_ctx.rows = session->term_rows;

    struct winsize new_ws = {
        .ws_row = session->term_rows,
        .ws_col = session->term_cols,
        .ws_xpixel = (unsigned short)(session->term_cols * session->cell_width),
        .ws_ypixel = (unsigned short)(session->term_rows * session->cell_height),
    };
    ioctl(session->pty_fd, TIOCSWINSZ, &new_ws);
    glyph_cache_clear();
    boo_session_render_frame(session);
    return 0;
}

bool boo_session_is_alive(const BooSession *session)
{
    return session && session->launched && !session->child_exited;
}

int boo_session_exit_status(const BooSession *session)
{
    if (!session || !session->child_exited || !session->child_reaped)
        return -1;
    return session->child_exit_status;
}

int boo_session_terminate(BooSession *session)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return -1;
    }

    boo_clear_error(session);
    if (!session->child_exited && session->child > 0)
        kill(session->child, SIGHUP);
    return 0;
}
