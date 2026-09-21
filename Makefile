.PHONY: install install-release release

PREFIX ?= $(HOME)/.local/bin

release:
	cargo build --release

install:
	cargo build --profile fast
	$(MAKE) PROFILE_DIR=fast do-install

install-release:
	cargo build --release
	$(MAKE) PROFILE_DIR=release do-install

.PHONY: do-install
# Installed builds are for testing, so each install starts with a fresh store.
do-install:
	mkdir -p $(PREFIX)
	cp target/$(PROFILE_DIR)/ox $(PREFIX)/.ox.tmp
	chmod 0755 $(PREFIX)/.ox.tmp
	mv -f $(PREFIX)/.ox.tmp $(PREFIX)/ox
	data_dir="$${OX_DATA_DIR:-$${XDG_DATA_HOME:-$$HOME/.local/share}/ox}"; \
		rm -f "$$data_dir/ox.db" "$$data_dir/ox.db-shm" "$$data_dir/ox.db-wal"
