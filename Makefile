.PHONY: format-docs inspect install

install:
	@./scripts/install.sh

format-docs:
	npx prettier --write docs/**/*.md

# Run ReSharper code inspections and write formatted results to inspection-results.txt.
# Requires: dotnet tool install -g JetBrains.ReSharper.GlobalTools
# Requires: jq
inspect:
	@jb inspectcode Ur.slnx -o=inspection-results.xml --severity=INFO --build > /dev/null 2>&1
	@./scripts/format-inspection-results.sh > inspection-results.txt
	@rm -f inspection-results.xml
	@echo "Wrote inspection-results.txt"
