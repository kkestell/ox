#include <signal.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

#include "boo_tester.h"

int main(int argc, char **argv)
{
    BooSession *session = boo_session_new();
    if (!session) {
        fprintf(stderr, "boo_session_new failed\n");
        return 1;
    }

    BooLaunchOptions opts = {
        .size = sizeof(opts),
        .cols = 80,
        .rows = 24,
        .font_size = 16,
        .padding = 4,
        .visible = true,
        .window_title = "boo",
    };

    const char *const *child_argv = NULL;
    if (argc > 1)
        child_argv = (const char *const *)&argv[1];

    if (boo_session_launch(session, child_argv, &opts) != 0) {
        fprintf(stderr, "launch failed: %s\n", boo_session_last_error(session));
        boo_session_free(session);
        return 1;
    }

    while (boo_session_is_alive(session)) {
        if (boo_session_step(session, 16) != 0) {
            fprintf(stderr, "step failed: %s\n", boo_session_last_error(session));
            boo_session_terminate(session);
            boo_session_free(session);
            return 1;
        }
    }

    boo_session_step(session, 0);

    int exit_status = boo_session_exit_status(session);
    boo_session_free(session);
    if (exit_status >= 0)
        return exit_status;
    return 0;
}
