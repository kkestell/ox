.PHONY: install release

release:
	cargo build --release

install:
	cargo build --release
	mkdir -p $(HOME)/.local/bin
	cp target/release/ox $(HOME)/.local/bin/.ox.tmp
	chmod 0755 $(HOME)/.local/bin/.ox.tmp
	mv -f $(HOME)/.local/bin/.ox.tmp $(HOME)/.local/bin/ox
