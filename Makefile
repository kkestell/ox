.PHONY: format-docs inspect install test verify evals-build evals-run evals-run-quick

install:
	@./scripts/install.sh

# Run all tests: dotnet unit tests, boo native + Python tests, and Boo-driven Ox smokes.
test:
	dotnet test Ox.slnx --nologo -v quiet
	$(MAKE) -C boo test
	cd boo && uv run python tests/test_ox_boo_smoke.py

# Alias for test.
verify: test

# Run ReSharper code inspections and write formatted results to inspection-results.txt.
# Requires: dotnet tool install -g JetBrains.ReSharper.GlobalTools
# Requires: jq
inspect:
	@jb inspectcode Ox.slnx -o=inspection-results.xml --severity=WARNING --build > /dev/null 2>&1
	@./scripts/format-inspection-results.sh > inspection-results.txt
	@rm -f inspection-results.xml
	@echo "Wrote inspection-results.txt"

run:
	@dotnet run --project src/Ox --no-build --nologo -- "$$ARGS"

# Build the eval container image. Contains the Ox binary + language toolchains.
evals-build:
	podman build -f evals/Containerfile -t ox-eval .

# Run all eval scenarios against their configured models.
evals-run: evals-build
	dotnet run --project evals/EvalRunner -- --scenarios evals/scenarios/

# Run only simple scenarios for fast pre-merge checks.
evals-run-quick: evals-build
	dotnet run --project evals/EvalRunner -- --scenarios evals/scenarios/ --complexity simple