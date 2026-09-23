.PHONY: install install-release release

PREFIX ?= $(HOME)/.local/bin
CONFIG_DIR ?= $(HOME)/.config/ox

release:
	cargo build --release

install:
	cargo build --profile fast
	$(MAKE) PROFILE_DIR=fast do-install

install-release:
	cargo build --release
	$(MAKE) PROFILE_DIR=release do-install

.PHONY: do-install
do-install:
	mkdir -p $(PREFIX)
	cp target/$(PROFILE_DIR)/ox $(PREFIX)/.ox.tmp
	chmod 0755 $(PREFIX)/.ox.tmp
	mv -f $(PREFIX)/.ox.tmp $(PREFIX)/ox
	mkdir -p $(CONFIG_DIR)
	cp examples/settings.json $(CONFIG_DIR)/settings.json
