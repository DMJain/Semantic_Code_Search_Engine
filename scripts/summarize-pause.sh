#!/bin/sh
# Interrupt the running `bin/indexer summarize` safely.
#
# The target is matched by an anchored regex on the full command line. The recipe's own command lines (sh -c, make, this script, caffeinate) all
# start with something other than "bin/indexer summarize", so none can match.
#
# Caveat: the default pattern assumes the run was launched as
# `bin/indexer summarize ...` (as `make summarize-run` does). A run launched
# any other way (absolute path, `go run`) is reported as "nothing running"
# even though it is alive; set SUMMARIZE_PAUSE_PATTERN or stop it by hand.
#
# Stopping: SIGINT is turned into context cancellation by signal.NotifyContext
# in cmd/indexer/main.go. The runner stops handing out new work and lets the
# item already in flight finish (processItem uses a non-cancelled context and
# writes its row done/failed) before the process exits. This script only
# verifies the process exited, not row contents.
#
# Exit codes: 0 stopped, 2 nothing running, 3 did not exit within the timeout,
# 4 more than one match (ambiguous; nothing is signalled).
#
# Env: SUMMARIZE_PAUSE_PATTERN (regex, default below; tests override it so a
# real run is never matched), SUMMARIZE_PAUSE_TIMEOUT (seconds, default 120).
pattern=${SUMMARIZE_PAUSE_PATTERN:-'^bin/indexer summarize( |$)'}
timeout=${SUMMARIZE_PAUSE_TIMEOUT:-120}

pids=$(pgrep -f "$pattern" | tr '\n' ' ')
pids=${pids% }

if [ -z "$pids" ]; then
	echo "summarize-pause: nothing running (no process matches '$pattern')"
	exit 2
fi
case "$pids" in
*" "*)
	echo "summarize-pause: ambiguous, several processes match ($pids); nothing signalled"
	exit 4
	;;
esac

echo "summarize-pause: sending SIGINT to pid $pids; the in-flight item finishes before exit"
kill -INT "$pids"

waited=0
while kill -0 "$pids" 2>/dev/null; do
	if [ "$waited" -ge "$timeout" ]; then
		echo "summarize-pause: pid $pids did not exit within ${timeout}s; it is still running"
		exit 3
	fi
	sleep 1
	waited=$((waited + 1))
done
echo "summarize-pause: pid $pids exited (the runner finishes its in-flight item on SIGINT; row contents are not checked here)"
exit 0
