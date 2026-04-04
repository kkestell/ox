cmake_minimum_required(VERSION 3.19)
project(boo C)

# Generate compile_commands.json for clangd/IDE support.
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)
include(CTest)

include(FetchContent)
find_package(Python3 REQUIRED COMPONENTS Interpreter)

set(BOO_PYTHON_MODULE_DIR "${CMAKE_SOURCE_DIR}/boo")

# --- SDL3 --------------------------------------------------------------------
find_package(SDL3 REQUIRED)
find_package(SDL3_ttf REQUIRED)

# --- libghostty-vt -----------------------------------------------------------
# Virtual terminal emulator library from Ghostty. Requires `zig` on PATH.
# The Ghostty CMakeLists.txt delegates to `zig build lib-vt` to produce the
# shared library. See https://ghostty.org/docs/install/build for the
# required Zig version.
FetchContent_Declare(ghostty
  GIT_REPOSITORY https://github.com/ghostty-org/ghostty.git
  GIT_TAG bebca84668947bfc92b9a30ed58712e1c34eee1d
)
FetchContent_MakeAvailable(ghostty)

# --- Our Project -------------------------------------------------------------
add_library(boo_tester SHARED src/boo_tester.c)
target_compile_features(boo_tester PRIVATE c_std_11)
target_compile_definitions(boo_tester PRIVATE BOO_PROJECT_DIR="${CMAKE_SOURCE_DIR}")
target_include_directories(boo_tester
  PUBLIC "${CMAKE_SOURCE_DIR}/src"
)
target_link_libraries(boo_tester SDL3::SDL3 SDL3_ttf::SDL3_ttf ghostty-vt)
add_custom_command(
  TARGET boo_tester POST_BUILD
  COMMAND ${CMAKE_COMMAND} -E copy_if_different
    $<TARGET_FILE:boo_tester>
    "${BOO_PYTHON_MODULE_DIR}/$<TARGET_FILE_NAME:boo_tester>"
)

add_executable(${PROJECT_NAME} src/main.c)
target_compile_features(${PROJECT_NAME} PRIVATE c_std_11)
target_include_directories(${PROJECT_NAME} PRIVATE "${CMAKE_SOURCE_DIR}/src")
target_link_libraries(${PROJECT_NAME} boo_tester)

if (BUILD_TESTING)
  add_executable(boo_native_smoke tests/native_smoke.c)
  target_compile_features(boo_native_smoke PRIVATE c_std_11)
  target_include_directories(boo_native_smoke PRIVATE "${CMAKE_SOURCE_DIR}/src")
  target_link_libraries(boo_native_smoke boo_tester)

  add_test(NAME native_smoke COMMAND boo_native_smoke)
  add_test(
    NAME python_smoke
    COMMAND ${CMAKE_COMMAND} -E env
      PYTHONPATH=${CMAKE_SOURCE_DIR}
      SDL_VIDEODRIVER=dummy
      ${Python3_EXECUTABLE} ${CMAKE_SOURCE_DIR}/tests/test_session.py
  )
endif()

# macOS requires Cocoa for SDL3's native window integration.
if (APPLE)
  target_link_libraries(boo_tester "-framework Cocoa")
  target_link_libraries(${PROJECT_NAME} "-framework Cocoa")
  if (BUILD_TESTING)
    target_link_libraries(boo_native_smoke "-framework Cocoa")
  endif()
endif()

# Linux requires libutil for forkpty().
if (UNIX AND NOT APPLE)
  target_link_libraries(boo_tester util)
  target_link_libraries(${PROJECT_NAME} util)
  if (BUILD_TESTING)
    target_link_libraries(boo_native_smoke util)
  endif()
endif()
