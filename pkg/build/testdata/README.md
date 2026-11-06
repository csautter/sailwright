# Zstd regression fixture

`zstd-two-block-panic.zst` is the 27,567-byte `oneFrame` input from
`TestDecoderHuffWeightsTableLog` in klauspost/compress v1.20.2 (BSD-3-Clause):
https://github.com/klauspost/compress/blob/v1.20.2/zstd/decoder_test.go

It reproduces GO-2026-6865 / GHSA-fpmh-frq8-7rj8: decoding the second block
reuses FSE state and panics on v1.20.1. The fixed decoder returns an error.
The download test serves it over a local HTTP server to exercise Sailwright's
go-getter decompression path and failed-download cleanup.

The upstream license is retained in `LICENSE.compress`:
https://github.com/klauspost/compress/blob/v1.20.2/LICENSE
