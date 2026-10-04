# What is collected

usage-control reads only what the operating system offers to programs without extra rights. It never changes the machine, runs no helper with privileges and starts no other programs, except `nvidia-smi` where it is installed. Values a system does not report are left out of the page instead of showing an error.

- [Where the values come from](#where-the-values-come-from)
- [The snapshot](#the-snapshot)
- [What each system reports](#what-each-system-reports)
- [GPUs](#gpus)
- [What is kept in the history](#what-is-kept-in-the-history)

## Where the values come from

| System | Source |
| --- | --- |
| Linux | `/proc` (CPU times, memory, load, disk and network counters, mounts) and `/sys` (temperatures, CPU clock, fans, batteries, GPUs, link speed, the Raspberry Pi's throttling flags), read with [gopsutil](https://github.com/shirou/gopsutil) and small file reads of its own |
| Linux in Docker | the same, from the host's `/proc` and `/sys`, mounted read-only at `/host/proc` and `/host/sys` (`HOST_PROC` and `HOST_SYS` point there), so the values describe the host, not the container |
| Windows | the Windows APIs gopsutil uses, and the performance counters Task Manager shows for GPUs |
| macOS | what gopsutil reports; not tested, low priority |

Readings are cheap: single file reads rather than scanning every process. Values that change rarely, such as a network card's link speed and addresses, are read once a minute. The machine is read only when asked, and at most once every 2 seconds however many pages are open: every 5 seconds for the history or a hub, every 2 seconds while a page is open (see [Inside one device](architecture.md#inside-one-device)).

CPU usage and disk and network speeds are measured between two readings, so they are averages over the last 2 seconds.

## The snapshot

`GET /api/metrics` answers with one snapshot. A hub receives the same JSON from each device.

| Field | Meaning |
| --- | --- |
| `name` | what the device calls itself: its `DEVICE_NAME`, or else its hostname; left out in Docker without `DEVICE_NAME`, where the hostname is the container's. A hub fills it in as the name when the device is added from its own browser |
| `version` | usage-control version that read it |
| `time`, `timeZone` | when it was read, and the device's time zone name and offset |
| `uptimeSeconds` | time since the machine started |
| `cpu` | `usagePercent`, `cores`, `coreUsagePercent` per core, `clockMHz`, `loadAverage` (1, 5, 15 min), `ioWaitPercent`, `stealPercent`, `processes` (total, running) |
| `memory` | `totalBytes`, `usedBytes`, `usedPercent`, `availableBytes`, `cachedBytes`, `swap` (total, used, percent) |
| `temperatures` | per sensor: `sensor`, `celsius` |
| `disks` | per path in `DISK_PATHS`: size, used, percent, read and write bytes per second, operations per second, busy percent, time per operation |
| `network` | per network card, without loopback: bytes received and sent in total and per second, errors, dropped packets, link speed, IPv4 addresses |
| `gpus` | per GPU: `name`, `usagePercent`, memory total and used, `celsius` |
| `throttling` | Raspberry Pi only: undervoltage, frequency capping and throttling, now and since boot |
| `battery` | `percent`, `pluggedIn`, `watts`, `healthPercent` |
| `fans` | per fan: `name`, `rpm` |

Sensors and GPUs that share a name are numbered (`coretemp 1`, `coretemp 2`), so every name stands for one sensor in the history.

## What each system reports

| Value | Linux | Windows | Kept in the history |
| --- | --- | --- | --- |
| CPU usage | yes | yes | yes |
| Usage per core | yes | yes | no, only the total |
| CPU clock | where the kernel scales it (not in most virtual machines) | no | no |
| Load average | yes | no, Windows has none | no |
| Processes (total and running) | yes | no | no |
| I/O wait and steal | yes | no | no |
| Memory used | yes | yes | yes |
| Available memory and cache | both | available only | no |
| Swap | when a swap file or partition exists | page files | yes |
| Disk usage | each path in `DISK_PATHS` | each drive in `DISK_PATHS` (default the system drive) | yes |
| Disk read/write speed | disks and partitions; for `/` inside Docker, the host's root disk | per drive letter | yes |
| Disk operations per second | yes | yes | no |
| Disk busy % and time per operation | yes | no | no |
| Network speed per card | yes | yes | yes |
| Network errors and dropped packets | since start | since start | no |
| Network link speed and IPv4 address | yes, read once a minute; most Wi-Fi cards report no speed | yes, read once a minute | no |
| Temperatures | where `/sys` has sensors (a Raspberry Pi has one for its CPU) | rarely, except NVIDIA GPUs through `nvidia-smi`; otherwise shown as unavailable | yes |
| GPU usage and memory | see [GPUs](#gpus) | see [GPUs](#gpus) | yes |
| Fan speed | where the kernel knows the fan, such as the Raspberry Pi 5 | no | no |
| Undervoltage and throttling | Raspberry Pi only | no | no |
| Battery charge and plugged in | laptops and tablets | laptops and tablets | yes, the charge |
| Battery power (W) and health | where the battery reports them | no | no |
| Time and time zone | yes; in Docker, the host's zone when `/etc/localtime` is mounted (as in `compose.yaml`), else UTC | yes | no |
| Uptime | yes | yes | no |

## GPUs

The GPU card appears when usage-control finds a GPU whose usage the system reports to programs without extra rights:

- **Windows:** every GPU, through the counters Task Manager shows, with its own memory. Windows has no GPU temperature for programs; the temperature of NVIDIA GPUs comes from `nvidia-smi`, which the NVIDIA driver installs, and is shown in the Temperature card under the GPU's name.
- **Linux:** AMD GPUs with usage, memory and temperature, and the Raspberry Pi's VideoCore GPU with usage, read from `/sys`. The Pi's GPU shares the main memory and its temperature is the Pi's CPU temperature, so only usage is shown. Older Raspberry Pi kernels do not report it; then the card stays hidden. NVIDIA GPUs show up when `nvidia-smi` is installed, which is not the case in the Docker image; its answer is reused for 4 seconds. Intel GPUs report their usage only to programs with extra rights, so they are not shown.
- **macOS:** not shown.

## What is kept in the history

The charts and the database keep a subset of the snapshot: the values that make sense as a line over time. Everything else is only shown live.

| Metric name | Unit | Per |
| --- | --- | --- |
| `cpu` | % | device |
| `memory` | % used | device |
| `swap` | % used | device, when it has swap |
| `battery` | % charge | device, when it has a battery |
| `temperature:<sensor>` | °C | sensor |
| `disk:<path>` | % used | disk |
| `disk.read:<path>`, `disk.write:<path>` | bytes per second | disk |
| `network.receive:<card>`, `network.send:<card>` | bytes per second | network card |
| `gpu:<name>` | % | GPU |
| `gpu.memory:<name>` | % used | GPU with its own memory |

Examples: `disk:/`, `network.receive:eth0`, `temperature:cpu_thermal`. Adding a metric needs no change to the database: every value is a row with its name.

Per device, the first 64 sensors, disks, network cards and GPUs each are kept (`HISTORY_MAX_ENTRIES`); a device that reports more is logged once, and the dashboard still shows them all.

How these values are stored, averaged and deleted is described in [Database and history](database.md).
