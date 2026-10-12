#!/bin/sh
# Interrupt the running `bin/indexer summarize` safely.
#
# The target is matched by an anchored regex on the full command line, so it
# cannot match this script's shell, make, or the keep-awake wrapper (their
# command lines start with sh/make/caffeinate, not "bin/indexer summarize").
# Own ancestors are also excluded as a second guard.
#
# Exit codes: 0 stopped, 2 nothing running, 3 did not exit within the timeout,
# 4 more than one match (ambiguous; nothing is signalled).
#
# Env: SUMMARIZE_PAUSE_PATTERN (regex, default below; tests override it so a
# real run is never matched), SUMMARIZE_PAUSE_TIMEOUT (seconds, default 120).
pattern=${SUMMARIZE_PAUSE_PATTERN:-'^bin/indexer summarize( |$)'}
timeout=${SUMMARIZE_PAUSE_TIMEOUT:-120}

# $$ and its ancestors, space-separated.
ancestors=" $$ "
p=$$
while [ -n "$p" ] && [ "$p" -gt 1 ]; do
	p=$(ps -o ppid= -p "$p" 2>/dev/null | tr -d ' ')
	[ -n "$p" ] && ancestors="$ancestors$p "
done

pids=""
for c in $(pgrep -f "$pattern"); do
	case "$ancestors" in *" $c "*) continue ;; esac
	pids="$pids $c"
done
pids=${pids# }

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
echo "summarize-pause: pid $pids exited; no row is left half-written (the in-flight item finished first)"
exit 0
