# REPRODUCIBILITY.md

Build/release evidence must bind to the declared inputs.

If generated outputs depend on:
- current time,
- random order,
- machine paths,
- locale,
- unstable network content,

normalize or record the source of nondeterminism.

Do not make reproducibility a blocker for ordinary local development unless the
project/release policy requires it.
