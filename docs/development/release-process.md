# Release process

MARSHAL uses Semantic Versioning for public product releases. Internal pack,
protocol, and database schema versions evolve independently and are documented
as separate compatibility contracts.

## Candidate gate

From a clean checkout:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
go build ./...
python3 tools/release_verify.py . distribution/PACK-MANIFEST.json
python3 -m unittest discover -s tools/tests -p 'test_*.py'
```

Run the Markdown link checker, vulnerability scanner, workflow linter, secret
review, and a clean installation smoke test. External provider tests are
reported separately because they depend on operator credentials and quota.

## Package

```bash
tools/build_release.sh v1.0.1 "$(git rev-parse HEAD)" \
  2026-08-18T00:00:00Z ./dist
```

Inspect archive contents, verify `checksums.txt`, inspect the SBOM, and run the
amd64 binary before tagging. The GitHub release workflow repeats the gate and
publishes artifacts only for `v*` tags.

## Publish

Create an annotated tag only after the branch CI is green. Push the tag, wait
for the release workflow, then download the public assets and repeat checksum,
version, and attestation verification. Release notes must list provider E2E
evidence and limitations without converting unavailable external services into
a passing result.
