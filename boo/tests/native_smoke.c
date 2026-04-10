#include <assert.h>
#include <stdlib.h>
#include <string.h>

#include "boo_tester.h"

static char *wait_for_text(BooSession *session, const char *needle)
{
    for (int i = 0; i < 80; i++) {
        assert(boo_session_step(session, 25) == 0);
        char *screen = boo_session_snapshot_text(session, true, false);
        assert(screen);
        if (strstr(screen, needle))
            return screen;
        boo_string_free(screen);
        if (!boo_session_is_alive(session))
            break;
    }
    return NULL;
}

int main(void)
{
    BooSession *session = boo_session_new();
    assert(session);

    const char *const argv[] = {
        "/bin/sh",
        "-lc",
        "printf ready; read x; printf seen:$x",
        NULL,
    };
    BooLaunchOptions opts = {
        .size = sizeof(opts),
        .cols = 80,
        .rows = 24,
        .font_size = 16,
        .padding = 4,
        .visible = false,
        .window_title = "native-smoke",
    };

    assert(boo_session_launch(session, argv, &opts) == 0);

    char *screen = wait_for_text(session, "ready");
    assert(screen);
    boo_string_free(screen);

    assert(boo_session_send_text(session, "hello\n") == 0);

    size_t input_len = 0;
    char *input = boo_session_snapshot_input(session, &input_len);
    assert(input);
    assert(input_len == strlen("hello\n"));
    assert(memcmp(input, "hello\n", input_len) == 0);
    boo_buffer_free(input);

    BooActivitySnapshot activity = { .size = sizeof(activity) };
    assert(boo_session_snapshot_activity(session, &activity) == 0);
    assert(activity.input_bytes == input_len);

    screen = wait_for_text(session, "seen:hello");
    assert(screen);
    boo_string_free(screen);

    for (int i = 0; i < 80 && boo_session_is_alive(session); i++)
        assert(boo_session_step(session, 25) == 0);

    for (int i = 0; i < 20 && boo_session_exit_status(session) < 0; i++)
        assert(boo_session_step(session, 10) == 0);

    assert(boo_session_exit_status(session) == 0);
    boo_session_free(session);
    return 0;
}
