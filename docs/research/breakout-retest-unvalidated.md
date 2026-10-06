# breakout_retest — unvalidated research note

`breakout_retest` (shared_strategies/open/breakout_retest.py) is a research
entry: after low-ATR compression it requires a volume-backed channel break
and enters only on a confirmed retest and reclaim. No breakout-bar chase.

Status: **unvalidated** — no fee audit, no held-out study, no paper
deployment. Registered with `edge_status="no_edge"` so live use requires an
explicit `allow_no_edge: true` acknowledgement and paper use requires an
explicit `--mode=paper`.
