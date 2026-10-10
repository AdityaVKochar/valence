#!/bin/sh
# Gives isolate a cgroups v2 subtree to manage, then runs the command.
#
# With --cgroupns=private the container sees its own cgroup as /sys/fs/cgroup. cgroups v2 only
# hands controllers down from a cgroup that has no processes of its own, so move everything
# into a leaf first, then create the subtree isolate puts its boxes in. Hosts that run
# isolate-cg-keeper and mount /run/isolate/cgroup into the container skip all of this.
set -eu

cg=/sys/fs/cgroup
# Without --privileged the cgroup filesystem is read-only; leave it alone so that commands
# which do not judge (judge-worker -version) still run, and the worker reports isolate unusable.
if [ ! -s /run/isolate/cgroup ] && [ -f "$cg/cgroup.controllers" ] && mkdir -p "$cg/init" "$cg/isolate" 2>/dev/null; then
	mkdir -p /run/isolate
	for pid in $(cat "$cg/cgroup.procs"); do
		echo "$pid" >"$cg/init/cgroup.procs" 2>/dev/null || true
	done
	for c in $(cat "$cg/cgroup.controllers"); do
		echo "+$c" >"$cg/cgroup.subtree_control" 2>/dev/null || true
		echo "+$c" >"$cg/isolate/cgroup.subtree_control" 2>/dev/null || true
	done
	echo "$cg/isolate" >/run/isolate/cgroup
fi
mkdir -p /run/isolate/locks

exec "$@"
