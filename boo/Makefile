CMAKE ?= cmake
BUILD_DIR ?= build

.PHONY: configure build test daemon clean distclean

configure:
	$(CMAKE) -S . -B $(BUILD_DIR)

build: configure
	$(CMAKE) --build $(BUILD_DIR)

test: build
	ctest --test-dir $(BUILD_DIR) --output-on-failure

daemon: build
	uv run boo start $(or $(CMD),bash)

clean:
	@if [ -d "$(BUILD_DIR)" ]; then $(CMAKE) --build $(BUILD_DIR) --target clean; fi

distclean:
	rm -rf $(BUILD_DIR)
	rm -f boo/libboo_tester.so boo/libboo_tester.dylib boo/boo_tester.dll
