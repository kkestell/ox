#ifndef BOO_TESTER_H
#define BOO_TESTER_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct BooSession BooSession;

typedef enum {
    BOO_KEY_UNIDENTIFIED = 0,
    BOO_KEY_BACKQUOTE,
    BOO_KEY_BACKSLASH,
    BOO_KEY_BRACKET_LEFT,
    BOO_KEY_BRACKET_RIGHT,
    BOO_KEY_COMMA,
    BOO_KEY_DIGIT_0,
    BOO_KEY_DIGIT_1,
    BOO_KEY_DIGIT_2,
    BOO_KEY_DIGIT_3,
    BOO_KEY_DIGIT_4,
    BOO_KEY_DIGIT_5,
    BOO_KEY_DIGIT_6,
    BOO_KEY_DIGIT_7,
    BOO_KEY_DIGIT_8,
    BOO_KEY_DIGIT_9,
    BOO_KEY_EQUAL,
    BOO_KEY_A,
    BOO_KEY_B,
    BOO_KEY_C,
    BOO_KEY_D,
    BOO_KEY_E,
    BOO_KEY_F,
    BOO_KEY_G,
    BOO_KEY_H,
    BOO_KEY_I,
    BOO_KEY_J,
    BOO_KEY_K,
    BOO_KEY_L,
    BOO_KEY_M,
    BOO_KEY_N,
    BOO_KEY_O,
    BOO_KEY_P,
    BOO_KEY_Q,
    BOO_KEY_R,
    BOO_KEY_S,
    BOO_KEY_T,
    BOO_KEY_U,
    BOO_KEY_V,
    BOO_KEY_W,
    BOO_KEY_X,
    BOO_KEY_Y,
    BOO_KEY_Z,
    BOO_KEY_MINUS,
    BOO_KEY_PERIOD,
    BOO_KEY_QUOTE,
    BOO_KEY_SEMICOLON,
    BOO_KEY_SLASH,
    BOO_KEY_BACKSPACE,
    BOO_KEY_ENTER,
    BOO_KEY_SPACE,
    BOO_KEY_TAB,
    BOO_KEY_DELETE,
    BOO_KEY_END,
    BOO_KEY_HOME,
    BOO_KEY_INSERT,
    BOO_KEY_PAGE_DOWN,
    BOO_KEY_PAGE_UP,
    BOO_KEY_ARROW_DOWN,
    BOO_KEY_ARROW_LEFT,
    BOO_KEY_ARROW_RIGHT,
    BOO_KEY_ARROW_UP,
    BOO_KEY_ESCAPE,
    BOO_KEY_F1,
    BOO_KEY_F2,
    BOO_KEY_F3,
    BOO_KEY_F4,
    BOO_KEY_F5,
    BOO_KEY_F6,
    BOO_KEY_F7,
    BOO_KEY_F8,
    BOO_KEY_F9,
    BOO_KEY_F10,
    BOO_KEY_F11,
    BOO_KEY_F12,
} BooKey;

typedef enum {
    BOO_MOUSE_BUTTON_UNKNOWN = 0,
    BOO_MOUSE_BUTTON_LEFT = 1,
    BOO_MOUSE_BUTTON_RIGHT = 2,
    BOO_MOUSE_BUTTON_MIDDLE = 3,
    BOO_MOUSE_BUTTON_X1 = 4,
    BOO_MOUSE_BUTTON_X2 = 5,
} BooMouseButton;

enum {
    BOO_MOD_SHIFT = 1 << 0,
    BOO_MOD_CTRL = 1 << 1,
    BOO_MOD_ALT = 1 << 2,
    BOO_MOD_SUPER = 1 << 3,
};

enum {
    BOO_KEY_ACTION_PRESS = 0,
    BOO_KEY_ACTION_RELEASE = 1,
    BOO_KEY_ACTION_REPEAT = 2,
    BOO_KEY_ACTION_PRESS_AND_RELEASE = 3,
};

typedef struct {
    size_t size;
    uint16_t cols;
    uint16_t rows;
    int font_size;
    int padding;
    const char *cwd;
    const char *const *env;
    bool visible;
    const char *window_title;
} BooLaunchOptions;

typedef struct {
    size_t size;
    uint64_t input_bytes;
    uint64_t output_bytes;
    uint64_t input_quiet_ms;
    uint64_t output_quiet_ms;
} BooActivitySnapshot;

BooSession *boo_session_new(void);
void boo_session_free(BooSession *session);

const char *boo_session_last_error(const BooSession *session);

int boo_session_launch(BooSession *session,
                             const char *const *argv,
                             const BooLaunchOptions *options);
int boo_session_step(BooSession *session, int timeout_ms);
int boo_session_send_bytes(BooSession *session, const void *data, size_t len);
int boo_session_send_text(BooSession *session, const char *utf8);
int boo_session_send_key(BooSession *session,
                               BooKey key,
                               uint16_t modifiers);
int boo_session_send_key_action(BooSession *session,
                               BooKey key,
                               uint16_t modifiers,
                               int action);
int boo_session_send_mouse_button(BooSession *session,
                                  int x,
                                  int y,
                                  BooMouseButton button,
                                  uint16_t modifiers,
                                  bool pressed);
int boo_session_send_mouse_move(BooSession *session,
                                int x,
                                int y,
                                uint16_t modifiers);
int boo_session_send_mouse_wheel(BooSession *session,
                                 int x,
                                 int y,
                                 int delta_x,
                                 int delta_y,
                                 uint16_t modifiers);
char *boo_session_snapshot_text(BooSession *session,
                                      bool trim,
                                      bool unwrap);
char *boo_session_snapshot_input(BooSession *session, size_t *len_out);
char *boo_session_snapshot_output(BooSession *session, size_t *len_out);
int boo_session_snapshot_activity(BooSession *session,
                                  BooActivitySnapshot *out_snapshot);
int boo_session_set_output_capture(BooSession *session, bool enabled);
void boo_buffer_free(void *data);
void boo_string_free(char *text);
int boo_session_resize(BooSession *session,
                             uint16_t cols,
                             uint16_t rows);
bool boo_session_is_alive(const BooSession *session);
int boo_session_exit_status(const BooSession *session);
int boo_session_terminate(BooSession *session);

#ifdef __cplusplus
}
#endif

#endif
