# Canonical ABI differential fixture generator

`gen.py` drives the pinned component-model reference model — `definitions.py` @
`2bed77e4228841c1d2721996d3ecc169ff96b158` (ADR 0084) — over `cases.json` and emits
`../testdata/fixtures.json`: for each case, the linear-memory byte image and realloc trace of a
`store`, and the flat core-value sequence of a `lower_flat`.

The fixtures are the oracle's reading, committed rather than regenerated in CI — the project's
external-oracle precedent (the Makefile `spec-images`/wabt target and its note). The Go codec's
differential test compares to the committed fixtures with **no Python in the loop**, so
`BURROUGHS_NO_SKIP=1` passes on a box with no interpreter. `definitions.py` is upstream spec material,
fetched into `vendor/` (gitignored), never committed.

Regenerate when `cases.json` or the pin changes:

```sh
make canon-fixtures
```

`uv` is the fleet's only Python (`uv run --no-project python3 gen.py`). `DETERMINISTIC_PROFILE = True`
makes the reference model's NaN handling reproducible.
