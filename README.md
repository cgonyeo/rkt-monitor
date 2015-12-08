# rkt-monitor

This is a small go utility intended to monitor the CPU and memory usage of rkt
and its children processes. This is accomplished by exec'ing rkt, reading proc
once a second for a specified duration, and printing the results.

Some acbuild scripts and golang source code is provided to build ACIs that
attempt to eat up resources in different ways.

Example output:

```
systemd-journal(6741): seconds alive: 9  avg CPU: 0%  avg Mem: 29 kB  peak Mem: 45 kB
worker(6746): seconds alive: 9  avg CPU: 23%  avg Mem: 238 kB  peak Mem: 360 kB
rkt(6688): seconds alive: 10  avg CPU: 0%  avg Mem: 26 kB  peak Mem: 27 kB
systemd(6740): seconds alive: 9  avg CPU: 0%  avg Mem: 35 kB  peak Mem: 35 kB
```
