# Reference frequency index

`corpusfreqlib/freq-index.json.gz` is the primary, tracked artifact used by `corpusfreqlib.Init()`.
The original 18 MB `corpusfreqlib/all.num` source is **not** shipped, which keeps clones small and
the single-binary default intact. Two tiny fixtures in `corpusfreqlib/testdata/` keep the loader
and generator tested offline.

To regenerate the index from your own copy of the BNC-style source data:

```sh
make freq-index FREQ_BNC=/path/to/all.num
# or
go run ./tools/generate_freq_index.go -bnc /path/to/all.num \
  -contemporary corpusfreqlib/contemporaryEnglish.txt \
  -output corpusfreqlib/freq-index.json.gz
```

The generator writes deterministic *content* (map order may change the compressed bytes); verify
with a decompressed JSON comparison if you need byte-level confidence. If the index is missing,
`Init()` fails with an actionable message instead of silently starting without contrast data.
