CMAKE ?= cmake
BUILD_DIR ?= build

.PHONY: configure build test run shell curses-app daemon clean distclean

configure:
	$(CMAKE) -S . -B $(BUILD_DIR)

build: configure
	$(CMAKE) --build $(BUILD_DIR)

test: build
	ctest --test-dir $(BUILD_DIR) --output-on-failure

run: build
	./$(BUILD_DIR)/boo

shell: build
	uv run python examples/shell.py

curses-app: build
	uv run python examples/curses_app.py

daemon: build
	uv run boo start $(or $(CMD),bash)

clean:
	@if [ -d "$(BUILD_DIR)" ]; then $(CMAKE) --build $(BUILD_DIR) --target clean; fi

distclean:
	rm -rf $(BUILD_DIR)
	rm -f boo/libboo_tester.so boo/libboo_tester.dylib boo/boo_tester.dll
