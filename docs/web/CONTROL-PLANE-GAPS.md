# Web Control Plane gaps

The v1.0.1 live-runtime boundary disables fixture-only API groups rather than
presenting synthetic state. Remaining gaps include:

- canonical task, agent, run, review, quorum, and merge handlers;
- real provider inventory and routing status;
- canonical memory governance queues, snapshots, usage traces, and security
  health summaries;
- canonical maintenance, settings, benchmark, and global-search handlers; and
- runtime event publication into the Web SSE hub.

These are follow-up items. Adaptive provider/resource control is not part of
the Community product boundary.
