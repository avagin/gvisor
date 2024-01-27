#!/bin/bash

set -xe

test -f /sys/fs/cgroup/devices/tasks || {
  mount -t tmpfs cgroups /sys/fs/cgroup
  mkdir /sys/fs/cgroup/devices
  mount -t cgroup -o devices devices /sys/fs/cgroup/devices
}

exec /usr/bin/dockerd --bridge=none --iptables=false --ip6tables=false "$@"
