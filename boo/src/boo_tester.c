#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pwd.h>
#include <signal.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#if defined(__APPLE__)
#include <util.h>
#else
#include <pty.h>
#endif

#include <ghostty/vt.h>

#include "boo_tester.h"

// The headless architecture is intentionally narrow: Boo owns a PTY, feeds its
// output into ghostty-vt, and uses ghostty's encoders to turn high-level key
// and mouse requests into terminal input bytes. There is no renderer, no GUI
// event pump, and no alternate control surface beyond the CLI daemon.

typedef struct {
    uint8_t *data;
    size_t len;
    size_t cap;
} BooByteBuffer;

typedef struct {
    BooSession *session;
    int cell_width;
    int cell_height;
    uint16_t cols;
    uint16_t rows;
} EffectsContext;

struct BooSession {
    int font_size;
    int pad;
    int cell_width;
    int cell_height;
    int scr_w;
    int scr_h;
    uint16_t term_cols;
    uint16_t term_rows;
    GhosttyTerminal terminal;
    pid_t child;
    int pty_fd;
    GhosttyKeyEncoder key_encoder;
    GhosttyKeyEvent key_event;
    GhosttyMouseEncoder mouse_encoder;
    GhosttyMouseEvent mouse_event;
    EffectsContext effects_ctx;
    BooByteBuffer input_bytes;
    BooByteBuffer output_bytes;
    uint64_t total_input_bytes;
    uint64_t total_output_bytes;
    uint64_t last_input_ms;
    uint64_t last_output_ms;
    uint16_t scripted_mouse_buttons;
    bool capture_output;
    bool launched;
    bool child_exited;
    bool child_reaped;
    bool pty_closed;
    int child_exit_status;
    char title[256];
    char last_error[512];
};

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

static uint64_t boo_now_ms(void)
{
    struct timespec ts = {0};
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000ULL + (uint64_t)ts.tv_nsec / 1000000ULL;
}

static void boo_sleep_ms(int timeout_ms)
{
    if (timeout_ms <= 0)
        return;

    struct timespec req = {
        .tv_sec = timeout_ms / 1000,
        .tv_nsec = (long)(timeout_ms % 1000) * 1000000L,
    };

    while (nanosleep(&req, &req) != 0 && errno == EINTR) {
    }
}

static void boo_set_errorf(BooSession *session, const char *fmt, ...);
static size_t boo_session_write_pty(BooSession *session, const void *data, size_t len);

static void boo_byte_buffer_reset(BooByteBuffer *buffer)
{
    if (!buffer)
        return;

    free(buffer->data);
    buffer->data = NULL;
    buffer->len = 0;
    buffer->cap = 0;
}

static bool boo_byte_buffer_append(BooByteBuffer *buffer, const void *data, size_t len)
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

static void boo_session_note_input(BooSession *session, const void *data, size_t len)
{
    if (!session || !data || len == 0)
        return;

    if (!boo_byte_buffer_append(&session->input_bytes, data, len)) {
        boo_set_errorf(session, "out of memory storing input transcript");
        return;
    }

    session->total_input_bytes += (uint64_t)len;
    session->last_input_ms = boo_now_ms();
}

static size_t boo_session_write_pty(BooSession *session, const void *data, size_t len)
{
    if (!session || !data || len == 0)
        return 0;

    size_t total_written = 0;
    size_t test_write_limit = 0;
    const char *test_limit_raw = getenv("BOO_TEST_WRITE_LIMIT");
    if (test_limit_raw && test_limit_raw[0] != '\0')
        test_write_limit = (size_t)strtoull(test_limit_raw, NULL, 10);

    const uint8_t *cursor = data;
    size_t remaining = len;
    while (remaining > 0) {
        size_t chunk_len = remaining;
        if (test_write_limit > 0) {
            if (total_written >= test_write_limit)
                break;
            size_t room = test_write_limit - total_written;
            if (chunk_len > room)
                chunk_len = room;
        }

        ssize_t written = write(session->pty_fd, cursor, chunk_len);
        if (written > 0) {
            boo_session_note_input(session, cursor, (size_t)written);
            cursor += written;
            remaining -= (size_t)written;
            total_written += (size_t)written;
            continue;
        }

        if (written < 0 && errno == EINTR)
            continue;

        break;
    }

    return total_written;
}

static int boo_session_require_full_write(
    BooSession *session,
    size_t written,
    size_t expected,
    const char *operation)
{
    if (written == expected)
        return 0;

    boo_set_errorf(
        session,
        "%s wrote %zu of %zu bytes; the PTY back-pressured the send",
        operation,
        written,
        expected);
    return -1;
}

static int boo_default_cell_width(int font_size)
{
    int width = font_size / 2;
    return width > 0 ? width : 8;
}

static int boo_default_cell_height(int font_size)
{
    return font_size > 0 ? font_size : 16;
}

// ---------------------------------------------------------------------------
// PTY lifecycle
// ---------------------------------------------------------------------------

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

static int pty_spawn(
    pid_t *child_out,
    uint16_t cols,
    uint16_t rows,
    int cell_width,
    int cell_height,
    const char *const *argv,
    const char *cwd,
    const char *const *env)
{
    int pty_fd = -1;
    struct winsize ws = {
        .ws_row = rows,
        .ws_col = cols,
        .ws_xpixel = (unsigned short)(cols * cell_width),
        .ws_ypixel = (unsigned short)(rows * cell_height),
    };

    pid_t child = forkpty(&pty_fd, NULL, NULL, &ws);
    if (child < 0)
        return -1;

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
        _exit(127);
    }

    int flags = fcntl(pty_fd, F_GETFL);
    if (flags < 0 || fcntl(pty_fd, F_SETFL, flags | O_NONBLOCK) < 0) {
        close(pty_fd);
        return -1;
    }

    *child_out = child;
    return pty_fd;
}

typedef enum {
    PTY_READ_OK,
    PTY_READ_EOF,
    PTY_READ_ERROR,
} PtyReadResult;

static PtyReadResult pty_read(
    int pty_fd,
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
                *last_output_ms = boo_now_ms();
            if (output_buffer && !boo_byte_buffer_append(output_buffer, buf, (size_t)n))
                return PTY_READ_ERROR;
            continue;
        }

        if (n == 0)
            return PTY_READ_EOF;

        if (errno == EAGAIN)
            return PTY_READ_OK;
        if (errno == EINTR)
            continue;
        if (errno == EIO)
            return PTY_READ_EOF;
        return PTY_READ_ERROR;
    }
}

// ---------------------------------------------------------------------------
// Key and mouse encoding
// ---------------------------------------------------------------------------

static GhosttyKey boo_key_to_ghostty(BooKey key)
{
    if (key >= BOO_KEY_A && key <= BOO_KEY_Z)
        return GHOSTTY_KEY_A + (key - BOO_KEY_A);
    if (key >= BOO_KEY_DIGIT_0 && key <= BOO_KEY_DIGIT_9)
        return GHOSTTY_KEY_DIGIT_0 + (key - BOO_KEY_DIGIT_0);
    if (key >= BOO_KEY_F1 && key <= BOO_KEY_F12)
        return GHOSTTY_KEY_F1 + (key - BOO_KEY_F1);

    switch (key) {
    case BOO_KEY_BACKQUOTE: return GHOSTTY_KEY_BACKQUOTE;
    case BOO_KEY_BACKSLASH: return GHOSTTY_KEY_BACKSLASH;
    case BOO_KEY_BRACKET_LEFT: return GHOSTTY_KEY_BRACKET_LEFT;
    case BOO_KEY_BRACKET_RIGHT: return GHOSTTY_KEY_BRACKET_RIGHT;
    case BOO_KEY_COMMA: return GHOSTTY_KEY_COMMA;
    case BOO_KEY_EQUAL: return GHOSTTY_KEY_EQUAL;
    case BOO_KEY_MINUS: return GHOSTTY_KEY_MINUS;
    case BOO_KEY_PERIOD: return GHOSTTY_KEY_PERIOD;
    case BOO_KEY_QUOTE: return GHOSTTY_KEY_QUOTE;
    case BOO_KEY_SEMICOLON: return GHOSTTY_KEY_SEMICOLON;
    case BOO_KEY_SLASH: return GHOSTTY_KEY_SLASH;
    case BOO_KEY_BACKSPACE: return GHOSTTY_KEY_BACKSPACE;
    case BOO_KEY_ENTER: return GHOSTTY_KEY_ENTER;
    case BOO_KEY_SPACE: return GHOSTTY_KEY_SPACE;
    case BOO_KEY_TAB: return GHOSTTY_KEY_TAB;
    case BOO_KEY_DELETE: return GHOSTTY_KEY_DELETE;
    case BOO_KEY_END: return GHOSTTY_KEY_END;
    case BOO_KEY_HOME: return GHOSTTY_KEY_HOME;
    case BOO_KEY_INSERT: return GHOSTTY_KEY_INSERT;
    case BOO_KEY_PAGE_DOWN: return GHOSTTY_KEY_PAGE_DOWN;
    case BOO_KEY_PAGE_UP: return GHOSTTY_KEY_PAGE_UP;
    case BOO_KEY_ARROW_DOWN: return GHOSTTY_KEY_ARROW_DOWN;
    case BOO_KEY_ARROW_LEFT: return GHOSTTY_KEY_ARROW_LEFT;
    case BOO_KEY_ARROW_RIGHT: return GHOSTTY_KEY_ARROW_RIGHT;
    case BOO_KEY_ARROW_UP: return GHOSTTY_KEY_ARROW_UP;
    case BOO_KEY_ESCAPE: return GHOSTTY_KEY_ESCAPE;
    default: return GHOSTTY_KEY_UNIDENTIFIED;
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
    case BOO_MOUSE_BUTTON_LEFT: return GHOSTTY_MOUSE_BUTTON_LEFT;
    case BOO_MOUSE_BUTTON_RIGHT: return GHOSTTY_MOUSE_BUTTON_RIGHT;
    case BOO_MOUSE_BUTTON_MIDDLE: return GHOSTTY_MOUSE_BUTTON_MIDDLE;
    case BOO_MOUSE_BUTTON_X1: return GHOSTTY_MOUSE_BUTTON_FOUR;
    case BOO_MOUSE_BUTTON_X2: return GHOSTTY_MOUSE_BUTTON_FIVE;
    default: return GHOSTTY_MOUSE_BUTTON_UNKNOWN;
    }
}

static uint16_t boo_mouse_button_to_mask(BooMouseButton button)
{
    switch (button) {
    case BOO_MOUSE_BUTTON_LEFT: return BOO_MOUSE_MASK_LEFT;
    case BOO_MOUSE_BUTTON_RIGHT: return BOO_MOUSE_MASK_RIGHT;
    case BOO_MOUSE_BUTTON_MIDDLE: return BOO_MOUSE_MASK_MIDDLE;
    case BOO_MOUSE_BUTTON_X1: return BOO_MOUSE_MASK_X1;
    case BOO_MOUSE_BUTTON_X2: return BOO_MOUSE_MASK_X2;
    default: return 0;
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
    case BOO_KEY_BACKQUOTE: return '`';
    case BOO_KEY_BACKSLASH: return '\\';
    case BOO_KEY_BRACKET_LEFT: return '[';
    case BOO_KEY_BRACKET_RIGHT: return ']';
    case BOO_KEY_COMMA: return ',';
    case BOO_KEY_EQUAL: return '=';
    case BOO_KEY_MINUS: return '-';
    case BOO_KEY_PERIOD: return '.';
    case BOO_KEY_QUOTE: return '\'';
    case BOO_KEY_SEMICOLON: return ';';
    case BOO_KEY_SLASH: return '/';
    case BOO_KEY_SPACE: return ' ';
    default: return 0;
    }
}

static uint32_t boo_key_to_shifted_codepoint(BooKey key)
{
    if (key >= BOO_KEY_A && key <= BOO_KEY_Z)
        return 'A' + (uint32_t)(key - BOO_KEY_A);

    switch (key) {
    case BOO_KEY_DIGIT_0: return ')';
    case BOO_KEY_DIGIT_1: return '!';
    case BOO_KEY_DIGIT_2: return '@';
    case BOO_KEY_DIGIT_3: return '#';
    case BOO_KEY_DIGIT_4: return '$';
    case BOO_KEY_DIGIT_5: return '%';
    case BOO_KEY_DIGIT_6: return '^';
    case BOO_KEY_DIGIT_7: return '&';
    case BOO_KEY_DIGIT_8: return '*';
    case BOO_KEY_DIGIT_9: return '(';
    case BOO_KEY_BACKQUOTE: return '~';
    case BOO_KEY_BACKSLASH: return '|';
    case BOO_KEY_BRACKET_LEFT: return '{';
    case BOO_KEY_BRACKET_RIGHT: return '}';
    case BOO_KEY_COMMA: return '<';
    case BOO_KEY_EQUAL: return '+';
    case BOO_KEY_MINUS: return '_';
    case BOO_KEY_PERIOD: return '>';
    case BOO_KEY_QUOTE: return '"';
    case BOO_KEY_SEMICOLON: return ':';
    case BOO_KEY_SLASH: return '?';
    case BOO_KEY_SPACE: return ' ';
    default: return boo_key_to_unshifted_codepoint(key);
    }
}

static int utf8_encode(uint32_t cp, char out[4])
{
    const uint32_t replacement_char = 0xFFFD;
    if (cp > 0x10FFFF)
        cp = replacement_char;

    if (cp < 0x80) {
        out[0] = (char)cp;
        return 1;
    }
    if (cp < 0x800) {
        out[0] = (char)(0xC0 | (cp >> 6));
        out[1] = (char)(0x80 | (cp & 0x3F));
        return 2;
    }
    if (cp < 0x10000) {
        out[0] = (char)(0xE0 | (cp >> 12));
        out[1] = (char)(0x80 | ((cp >> 6) & 0x3F));
        out[2] = (char)(0x80 | (cp & 0x3F));
        return 3;
    }

    out[0] = (char)(0xF0 | (cp >> 18));
    out[1] = (char)(0x80 | ((cp >> 12) & 0x3F));
    out[2] = (char)(0x80 | ((cp >> 6) & 0x3F));
    out[3] = (char)(0x80 | (cp & 0x3F));
    return 4;
}

static int boo_session_mouse_position(
    BooSession *session,
    int x,
    int y,
    GhosttyMousePosition *out_position)
{
    if (x < 0 || y < 0 || x >= session->term_cols || y >= session->term_rows) {
        boo_set_errorf(
            session,
            "mouse coordinates (%d, %d) must be within the terminal grid",
            x,
            y);
        return -1;
    }

    out_position->x = (float)(session->pad + x * session->cell_width + session->cell_width / 2);
    out_position->y = (float)(session->pad + y * session->cell_height + session->cell_height / 2);
    return 0;
}

static void boo_session_configure_mouse_encoder(
    BooSession *session,
    GhosttyMousePosition position,
    GhosttyMods mods)
{
    ghostty_mouse_encoder_setopt_from_terminal(session->mouse_encoder, session->terminal);
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
    ghostty_mouse_encoder_setopt(
        session->mouse_encoder,
        GHOSTTY_MOUSE_ENCODER_OPT_SIZE,
        &enc_size);

    bool any_pressed = session->scripted_mouse_buttons != 0;
    ghostty_mouse_encoder_setopt(
        session->mouse_encoder,
        GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED,
        &any_pressed);

    bool track_cell = true;
    ghostty_mouse_encoder_setopt(
        session->mouse_encoder,
        GHOSTTY_MOUSE_ENCODER_OPT_TRACK_LAST_CELL,
        &track_cell);
}

static void mouse_encode_and_write(
    BooSession *session,
    GhosttyMouseEncoder encoder,
    GhosttyMouseEvent event)
{
    char buf[128];
    size_t written = 0;
    GhosttyResult res = ghostty_mouse_encoder_encode(encoder, event, buf, sizeof(buf), &written);
    if (res == GHOSTTY_SUCCESS && written > 0)
        boo_session_write_pty(session, buf, written);
}

static int boo_send_key_with_action(
    BooSession *session,
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
        uint32_t codepoint = (mods & GHOSTTY_MODS_SHIFT)
            ? boo_key_to_shifted_codepoint(key)
            : unshifted;
        utf8_len = (size_t)utf8_encode(codepoint, utf8_buf);
        if (mods & GHOSTTY_MODS_SHIFT)
            consumed |= GHOSTTY_MODS_SHIFT;
    }

    ghostty_key_event_set_consumed_mods(session->key_event, consumed);
    if (utf8_len > 0)
        ghostty_key_event_set_utf8(session->key_event, utf8_buf, utf8_len);
    else
        ghostty_key_event_set_utf8(session->key_event, NULL, 0);

    char buf[128];
    size_t encoded = 0;
    GhosttyResult res = ghostty_key_encoder_encode(
        session->key_encoder,
        session->key_event,
        buf,
        sizeof(buf),
        &encoded);
    if (res != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_key_encoder_encode failed (%d)", res);
        return -1;
    }

    size_t written = boo_session_write_pty(session, buf, encoded);
    return boo_session_require_full_write(session, written, encoded, "send_key");
}

// ---------------------------------------------------------------------------
// Terminal effect callbacks
// ---------------------------------------------------------------------------

static void effect_write_pty(GhosttyTerminal terminal, void *userdata, const uint8_t *data, size_t len)
{
    (void)terminal;
    EffectsContext *ctx = (EffectsContext *)userdata;
    boo_session_write_pty(ctx->session, data, len);
}

static bool effect_size(GhosttyTerminal terminal, void *userdata, GhosttySizeReportSize *out_size)
{
    (void)terminal;
    EffectsContext *ctx = (EffectsContext *)userdata;
    out_size->rows = ctx->rows;
    out_size->columns = ctx->cols;
    out_size->cell_width = (uint32_t)ctx->cell_width;
    out_size->cell_height = (uint32_t)ctx->cell_height;
    return true;
}

static bool effect_device_attributes(
    GhosttyTerminal terminal,
    void *userdata,
    GhosttyDeviceAttributes *out_attrs)
{
    (void)terminal;
    (void)userdata;

    out_attrs->primary.conformance_level = GHOSTTY_DA_CONFORMANCE_VT220;
    out_attrs->primary.features[0] = GHOSTTY_DA_FEATURE_COLUMNS_132;
    out_attrs->primary.features[1] = GHOSTTY_DA_FEATURE_SELECTIVE_ERASE;
    out_attrs->primary.features[2] = GHOSTTY_DA_FEATURE_ANSI_COLOR;
    out_attrs->primary.num_features = 3;

    out_attrs->secondary.device_type = GHOSTTY_DA_DEVICE_TYPE_VT220;
    out_attrs->secondary.firmware_version = 1;
    out_attrs->secondary.rom_cartridge = 0;

    out_attrs->tertiary.unit_id = 0;
    return true;
}

static GhosttyString effect_xtversion(GhosttyTerminal terminal, void *userdata)
{
    (void)terminal;
    (void)userdata;
    return (GhosttyString){ .ptr = (const uint8_t *)"boo", .len = 3 };
}

static void effect_title_changed(GhosttyTerminal terminal, void *userdata)
{
    EffectsContext *ctx = (EffectsContext *)userdata;
    BooSession *session = ctx->session;
    GhosttyString title = {0};
    if (ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_TITLE, &title) != GHOSTTY_SUCCESS)
        return;

    size_t len = title.len < sizeof(session->title) - 1 ? title.len : sizeof(session->title) - 1;
    memcpy(session->title, title.ptr, len);
    session->title[len] = '\0';
}

static bool effect_color_scheme(GhosttyTerminal terminal, void *userdata, GhosttyColorScheme *out_scheme)
{
    (void)terminal;
    (void)userdata;
    (void)out_scheme;
    return false;
}

// ---------------------------------------------------------------------------
// Session lifecycle
// ---------------------------------------------------------------------------

static void boo_session_cleanup(BooSession *session)
{
    if (!session)
        return;

    boo_byte_buffer_reset(&session->input_bytes);
    boo_byte_buffer_reset(&session->output_bytes);

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
    if (session->terminal) {
        ghostty_terminal_free(session->terminal);
        session->terminal = NULL;
    }

    session->launched = false;
    session->child_exited = false;
    session->child_reaped = false;
    session->pty_closed = false;
    session->child_exit_status = -1;
    session->font_size = 0;
    session->pad = 0;
    session->cell_width = 0;
    session->cell_height = 0;
    session->scr_w = 0;
    session->scr_h = 0;
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
    session->title[0] = '\0';
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

int boo_session_launch(BooSession *session, const char *const *argv, const BooLaunchOptions *options)
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
        .visible = false,
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

    session->font_size = cfg.font_size;
    session->pad = cfg.padding;
    session->cell_width = boo_default_cell_width(cfg.font_size);
    session->cell_height = boo_default_cell_height(cfg.font_size);
    session->term_cols = cfg.cols;
    session->term_rows = cfg.rows;
    session->scr_w = session->term_cols * session->cell_width + 2 * session->pad;
    session->scr_h = session->term_rows * session->cell_height + 2 * session->pad;

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

    session->pty_fd = pty_spawn(
        &session->child,
        session->term_cols,
        session->term_rows,
        session->cell_width,
        session->cell_height,
        argv,
        cfg.cwd,
        cfg.env);
    if (session->pty_fd < 0) {
        boo_set_errorf(session, "pty_spawn failed: %s", strerror(errno));
        boo_session_cleanup(session);
        return -1;
    }

    session->effects_ctx = (EffectsContext){
        .session = session,
        .cell_width = session->cell_width,
        .cell_height = session->cell_height,
        .cols = session->term_cols,
        .rows = session->term_rows,
    };
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_USERDATA, &session->effects_ctx);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_WRITE_PTY, (const void *)effect_write_pty);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_SIZE, (const void *)effect_size);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_DEVICE_ATTRIBUTES, (const void *)effect_device_attributes);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_XTVERSION, (const void *)effect_xtversion);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_TITLE_CHANGED, (const void *)effect_title_changed);
    ghostty_terminal_set(session->terminal, GHOSTTY_TERMINAL_OPT_COLOR_SCHEME, (const void *)effect_color_scheme);

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

    size_t title_len = strlen(cfg.window_title);
    if (title_len >= sizeof(session->title))
        title_len = sizeof(session->title) - 1;
    memcpy(session->title, cfg.window_title, title_len);
    session->title[title_len] = '\0';

    session->launched = true;
    session->child_exited = false;
    session->child_reaped = false;
    session->pty_closed = false;
    session->child_exit_status = -1;
    session->last_input_ms = boo_now_ms();
    session->last_output_ms = session->last_input_ms;
    session->capture_output = false;
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

    uint64_t deadline = boo_now_ms() + (uint64_t)timeout_ms;
    do {
        int slice_ms = 0;
        if (timeout_ms > 0) {
            uint64_t now = boo_now_ms();
            if (now >= deadline)
                break;
            uint64_t remaining = deadline - now;
            slice_ms = remaining > 10 ? 10 : (int)remaining;
        }

        if (!session->pty_closed && session->pty_fd >= 0) {
            struct pollfd pfd = {
                .fd = session->pty_fd,
                .events = POLLIN | POLLHUP | POLLERR,
            };
            int poll_rc = poll(&pfd, 1, slice_ms);
            if (poll_rc < 0) {
                if (errno == EINTR)
                    continue;
                boo_set_errorf(session, "poll failed: %s", strerror(errno));
                return -1;
            }

            if (poll_rc > 0 || timeout_ms == 0) {
                PtyReadResult pty_rc = pty_read(
                    session->pty_fd,
                    session->terminal,
                    session->capture_output ? &session->output_bytes : NULL,
                    &session->total_output_bytes,
                    &session->last_output_ms);
                if (pty_rc == PTY_READ_EOF) {
                    session->pty_closed = true;
                    session->child_exited = true;
                } else if (pty_rc == PTY_READ_ERROR) {
                    boo_set_errorf(session, "failed to read PTY output");
                    return -1;
                }
            }
        } else if (slice_ms > 0) {
            boo_sleep_ms(slice_ms);
        }

        if (!session->child_reaped && session->child > 0) {
            int wstatus = 0;
            pid_t wp = waitpid(session->child, &wstatus, WNOHANG);
            if (wp > 0) {
                session->child_reaped = true;
                session->child_exited = true;
                if (WIFEXITED(wstatus))
                    session->child_exit_status = WEXITSTATUS(wstatus);
                else if (WIFSIGNALED(wstatus))
                    session->child_exit_status = 128 + WTERMSIG(wstatus);
            } else if (wp < 0 && errno == ECHILD) {
                session->child_reaped = true;
                session->child_exited = true;
            }
        }

        if (timeout_ms == 0)
            break;
        if (session->child_reaped && session->pty_closed)
            break;
    } while (boo_now_ms() < deadline);

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

int boo_session_send_key(BooSession *session, BooKey key, uint16_t modifiers)
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
    if (rc != 0)
        return rc;
    return boo_send_key_with_action(session, key, modifiers, GHOSTTY_KEY_ACTION_RELEASE);
}

int boo_session_send_key_action(BooSession *session, BooKey key, uint16_t modifiers, int action)
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
        if (rc != 0)
            return rc;
        return boo_send_key_with_action(session, key, modifiers, GHOSTTY_KEY_ACTION_RELEASE);
    }

    GhosttyKeyAction gaction;
    switch (action) {
    case BOO_KEY_ACTION_RELEASE:
        gaction = GHOSTTY_KEY_ACTION_RELEASE;
        break;
    case BOO_KEY_ACTION_REPEAT:
        gaction = GHOSTTY_KEY_ACTION_REPEAT;
        break;
    default:
        gaction = GHOSTTY_KEY_ACTION_PRESS;
        break;
    }

    return boo_send_key_with_action(session, key, modifiers, gaction);
}

int boo_session_send_mouse_button(
    BooSession *session,
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
    ghostty_mouse_event_set_action(
        session->mouse_event,
        pressed ? GHOSTTY_MOUSE_ACTION_PRESS : GHOSTTY_MOUSE_ACTION_RELEASE);
    ghostty_mouse_event_set_button(session->mouse_event, gbutton);
    mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
    return 0;
}

int boo_session_send_mouse_move(BooSession *session, int x, int y, uint16_t modifiers)
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

int boo_session_send_mouse_wheel(
    BooSession *session,
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
    ghostty_terminal_get(session->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &mouse_tracking);
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

    for (int remaining = delta_y; remaining != 0;) {
        GhosttyMouseButton button = remaining > 0 ? GHOSTTY_MOUSE_BUTTON_FOUR : GHOSTTY_MOUSE_BUTTON_FIVE;
        ghostty_mouse_event_set_button(session->mouse_event, button);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_PRESS);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_RELEASE);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        remaining += remaining > 0 ? -1 : 1;
    }

    for (int remaining = delta_x; remaining != 0;) {
        GhosttyMouseButton button = remaining > 0 ? GHOSTTY_MOUSE_BUTTON_SIX : GHOSTTY_MOUSE_BUTTON_SEVEN;
        ghostty_mouse_event_set_button(session->mouse_event, button);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_PRESS);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        ghostty_mouse_event_set_action(session->mouse_event, GHOSTTY_MOUSE_ACTION_RELEASE);
        mouse_encode_and_write(session, session->mouse_encoder, session->mouse_event);
        remaining += remaining > 0 ? -1 : 1;
    }

    return 0;
}

char *boo_session_snapshot_text(BooSession *session, bool trim, bool unwrap)
{
    if (!session || !session->launched) {
        boo_set_errorf(session, "session has not been launched");
        return NULL;
    }

    boo_clear_error(session);

    GhosttyFormatterTerminalOptions opts = GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalOptions);
    opts.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
    opts.trim = trim;
    opts.unwrap = unwrap;

    GhosttyFormatter formatter = NULL;
    GhosttyResult res = ghostty_formatter_terminal_new(NULL, &formatter, session->terminal, opts);
    if (res != GHOSTTY_SUCCESS) {
        boo_set_errorf(session, "ghostty_formatter_terminal_new failed (%d)", res);
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

int boo_session_snapshot_activity(BooSession *session, BooActivitySnapshot *out_snapshot)
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

int boo_session_resize(BooSession *session, uint16_t cols, uint16_t rows)
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

    ghostty_terminal_resize(
        session->terminal,
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
    return 0;
}

bool boo_session_is_alive(const BooSession *session)
{
    return session && session->launched && !session->child_exited;
}

int boo_session_exit_status(const BooSession *session)
{
    if (!session || !session->child_reaped)
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
