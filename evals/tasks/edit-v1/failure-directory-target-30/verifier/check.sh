test -d archive
test "$(find archive -type f | wc -l | tr -d ' ')" = 1
