import { Device, LOCAL_DEVICE } from '../app/devices/devices';
import { Snapshot, Throttling, TimeZone } from '../app/metrics/metrics';

/**
 * The made-up devices of the demo: a Raspberry Pi as the hub, and Windows and
 * Linux PCs and servers on the same network. Each one reports what the real
 * backend reports on that kind of machine, from values that drift smoothly
 * over time, so the live values and the history agree.
 */

const GB = 1024 ** 3;
const TB = 1024 ** 4;

/** The values of one moment, keyed like the history's series: "cpu", "disk:/", "network.receive:eth0" and so on. */
export type Values = Record<string, number>;

export interface DemoMachine {
  device: Device;
  os: 'linux' | 'windows';
  cores: number;
  maxClockMHz: number;
  memoryBytes: number;
  swapBytes?: number;
  disks: { path: string; totalBytes: number }[];
  /** Interfaces without traffic values are listed as idle, like the extra adapters of Windows PCs. */
  network: { name: string; addresses?: string[]; linkMbps?: number }[];
  gpus?: { name: string; memoryBytes?: number }[];
  fans?: string[];
  throttling?: Throttling;
  /** Linux laptops report the battery's power and health too. */
  batteryDetails?: boolean;
  /** Servers keep their clocks in UTC; the other devices use the visitor's time zone. */
  utc?: boolean;
  bootedDaysAgo: number;
  /** For a device that answers only part of the day, such as a laptop that sleeps at night. */
  online?: (t: number) => boolean;
  /**
   * The values at t, in Unix seconds. With a step, as for a long history range, changes
   * much faster than the step are left out, since averages over the step smooth them out.
   */
  values: (t: number, step: number) => Values;
}

/** When the page was opened, in Unix seconds. */
const openedAt = Math.floor(Date.now() / 1000);

/** A number from 0 to 1 for each whole number i, the same every time. */
function hash(i: number, seed: number): number {
  let h = Math.imul(i ^ Math.imul(seed, 0x9e3779b1), 0x27d4eb2d);
  h ^= h >>> 15;
  h = Math.imul(h, 0x85ebca6b);
  h ^= h >>> 13;
  return (h >>> 0) / 4294967296;
}

/** A value from -1 to 1 that changes smoothly, about once per period seconds. */
export function drift(t: number, period: number, seed: number): number {
  const x = t / period;
  const i = Math.floor(x);
  const f = x - i;
  const s = f * f * (3 - 2 * f);
  return (hash(i, seed) * (1 - s) + hash(i + 1, seed) * s) * 2 - 1;
}

/** base plus several drifts, each [amplitude, period], kept within min and max. */
function vary(
  t: number,
  step: number,
  seed: number,
  base: number,
  swings: [number, number][],
  min = 0,
  max = 100,
): number {
  const value = swings.reduce(
    (sum, [amplitude, period], i) =>
      sum + amplitude * Math.min(1, period / Math.max(1, step)) * drift(t, period, seed * 31 + i),
    base,
  );
  return Math.min(max, Math.max(min, value));
}

/** The hour of the day at t, from 0 to 24, in the visitor's time zone. */
function hourOf(t: number): number {
  return ((((t - new Date(t * 1000).getTimezoneOffset() * 60) / 3600) % 24) + 24) % 24;
}

/** 1 in the middle of the working day and 0 at night. */
function workday(t: number): number {
  return Math.max(0, Math.sin(((hourOf(t) - 7) / 12) * Math.PI));
}

/** A battery that runs down over about four hours and charges in about one and a half. */
function batteryPercent(t: number): number {
  const cycle = 5.5 * 3600;
  const phase = (((t + 9000) % cycle) + cycle) % cycle;
  const discharge = 4 * 3600;
  return phase < discharge
    ? 98 - (phase / discharge) * 78
    : 20 + ((phase - discharge) / (cycle - discharge)) * 78;
}

function charging(t: number): boolean {
  return batteryPercent(t + 60) > batteryPercent(t);
}

const piHub: DemoMachine = {
  device: LOCAL_DEVICE,
  os: 'linux',
  cores: 4,
  maxClockMHz: 2400,
  memoryBytes: 8 * GB,
  swapBytes: 0.5 * GB,
  disks: [
    { path: '/', totalBytes: 117 * GB },
    { path: '/mnt/usb', totalBytes: 932 * GB },
  ],
  network: [{ name: 'eth0', addresses: ['192.168.1.10'], linkMbps: 1000 }, { name: 'wlan0' }],
  gpus: [{ name: 'VideoCore VII' }],
  fans: ['pwmfan'],
  throttling: { now: [], sinceBoot: ['softTemperatureLimit'] },
  bootedDaysAgo: 12.3,
  values: (t, step) => {
    const cpu = vary(
      t,
      step,
      1,
      9,
      [
        [5, 40],
        [4, 600],
        [3, 7200],
      ],
      1,
    );
    return {
      cpu,
      memory: vary(t, step, 2, 38, [
        [3, 900],
        [2, 86400],
      ]),
      swap: vary(t, step, 3, 4, [[2, 86400]]),
      'temperature:cpu_thermal': 44 + cpu * 0.35 + vary(t, step, 4, 0, [[2, 300]], -5, 5),
      'disk:/': vary(t, step, 5, 21, [[0.3, 86400 * 5]]),
      'disk:/mnt/usb': vary(t, step, 6, 46, [[1, 86400 * 9]]),
      'disk.read:/': vary(t, step, 7, 40e3, [[35e3, 60]], 0, 1e9),
      'disk.write:/': vary(t, step, 8, 180e3, [[150e3, 30]], 0, 1e9),
      'disk.read:/mnt/usb': vary(t, step, 9, 5e3, [[5e3, 120]], 0, 1e9),
      'disk.write:/mnt/usb': vary(t, step, 10, 60e3, [[55e3, 90]], 0, 1e9),
      'network.receive:eth0': vary(
        t,
        step,
        11,
        90e3,
        [
          [60e3, 30],
          [30e3, 900],
        ],
        0,
        1e9,
      ),
      'network.send:eth0': vary(t, step, 12, 35e3, [[25e3, 45]], 0, 1e9),
      'gpu:VideoCore VII': vary(t, step, 13, 3, [[3, 120]]),
    };
  },
};

const windowsPc: DemoMachine = {
  device: { id: 'windows-pc', name: 'Windows PC', address: '192.168.1.21:9393', removable: true },
  os: 'windows',
  cores: 16,
  maxClockMHz: 4700,
  memoryBytes: 32 * GB,
  swapBytes: 4.75 * GB,
  disks: [
    { path: 'C:\\', totalBytes: 931 * GB },
    { path: 'D:\\', totalBytes: 1.82 * TB },
  ],
  network: [
    { name: 'Ethernet', addresses: ['192.168.1.21'], linkMbps: 2500 },
    { name: 'WLAN', addresses: ['192.168.1.121'] },
    { name: 'Bluetooth-Netzwerkverbindung' },
    { name: 'vEthernet (WSL)', addresses: ['172.23.80.1'] },
    { name: 'OpenVPN Data Channel Offload' },
  ],
  gpus: [{ name: 'NVIDIA GeForce RTX 4070', memoryBytes: 12 * GB }],
  bootedDaysAgo: 0.4,
  values: (t, step) => {
    const busy = workday(t);
    const gpu = vary(t, step, 21, 8 + 35 * busy, [
      [25, 1800],
      [10, 60],
    ]);
    return {
      cpu: vary(
        t,
        step,
        22,
        6 + 22 * busy,
        [
          [12, 30],
          [8, 900],
        ],
        1,
      ),
      memory: vary(t, step, 23, 41 + 15 * busy, [[6, 1200]]),
      swap: vary(t, step, 24, 11, [[4, 7200]]),
      'disk:C:\\': vary(t, step, 25, 63, [[0.5, 86400 * 4]]),
      'disk:D:\\': vary(t, step, 26, 78, [[1, 86400 * 7]]),
      'disk.read:C:\\': vary(t, step, 27, 2e6 * busy + 300e3, [[1.5e6, 20]], 0, 2e9),
      'disk.write:C:\\': vary(t, step, 28, 1e6 * busy + 200e3, [[900e3, 25]], 0, 2e9),
      'disk.read:D:\\': vary(t, step, 29, 400e3 * busy, [[3e6, 300]], 0, 2e9),
      'disk.write:D:\\': vary(t, step, 30, 100e3, [[500e3, 400]], 0, 2e9),
      'network.receive:Ethernet': vary(
        t,
        step,
        31,
        600e3 + 3e6 * busy,
        [
          [2.5e6, 120],
          [500e3, 15],
        ],
        0,
        3e8,
      ),
      'network.send:Ethernet': vary(t, step, 32, 120e3 + 300e3 * busy, [[200e3, 60]], 0, 3e8),
      'network.receive:vEthernet (WSL)': vary(t, step, 33, 4e3, [[4e3, 300]], 0, 1e9),
      'network.send:vEthernet (WSL)': vary(t, step, 34, 2e3, [[2e3, 300]], 0, 1e9),
      'gpu:NVIDIA GeForce RTX 4070': gpu,
      'gpu.memory:NVIDIA GeForce RTX 4070': 18 + gpu * 0.5,
    };
  },
};

const windowsLaptop: DemoMachine = {
  device: {
    id: 'windows-laptop',
    name: 'Windows laptop',
    address: '192.168.1.34:9393',
    removable: true,
  },
  os: 'windows',
  cores: 12,
  maxClockMHz: 3400,
  memoryBytes: 16 * GB,
  swapBytes: 2.5 * GB,
  disks: [{ path: 'C:\\', totalBytes: 476 * GB }],
  network: [
    { name: 'WLAN', addresses: ['192.168.1.34'] },
    { name: 'Bluetooth-Netzwerkverbindung' },
    { name: 'LAN-Verbindung* 1' },
  ],
  gpus: [{ name: 'Qualcomm Adreno X1-85 GPU' }],
  bootedDaysAgo: 2.1,
  values: (t, step) => ({
    cpu: vary(
      t,
      step,
      41,
      14,
      [
        [10, 20],
        [8, 600],
      ],
      1,
    ),
    memory: vary(t, step, 42, 58, [[7, 1500]]),
    swap: vary(t, step, 43, 9, [[3, 7200]]),
    battery: batteryPercent(t),
    'disk:C:\\': vary(t, step, 44, 52, [[0.5, 86400 * 3]]),
    'disk.read:C:\\': vary(t, step, 45, 700e3, [[600e3, 20]], 0, 2e9),
    'disk.write:C:\\': vary(t, step, 46, 400e3, [[350e3, 30]], 0, 2e9),
    'network.receive:WLAN': vary(
      t,
      step,
      47,
      400e3,
      [
        [350e3, 40],
        [200e3, 600],
      ],
      0,
      1e9,
    ),
    'network.send:WLAN': vary(t, step, 48, 60e3, [[50e3, 30]], 0, 1e9),
    'gpu:Qualcomm Adreno X1-85 GPU': vary(t, step, 49, 9, [[7, 90]]),
  }),
};

const windowsServer: DemoMachine = {
  device: { id: 'windows-server', name: 'Windows Server', address: '192.168.1.5:9393' },
  os: 'windows',
  cores: 8,
  maxClockMHz: 3000,
  memoryBytes: 64 * GB,
  swapBytes: 8 * GB,
  disks: [
    { path: 'C:\\', totalBytes: 237 * GB },
    { path: 'E:\\', totalBytes: 7.28 * TB },
  ],
  network: [
    { name: 'Ethernet 1', addresses: ['192.168.1.5'], linkMbps: 10000 },
    { name: 'Ethernet 2', addresses: ['10.0.0.5'], linkMbps: 10000 },
    { name: 'Ethernet 3' },
    { name: 'Ethernet 4' },
  ],
  utc: true,
  bootedDaysAgo: 23.6,
  values: (t, step) => {
    const busy = workday(t);
    return {
      cpu: vary(
        t,
        step,
        51,
        12 + 18 * busy,
        [
          [8, 60],
          [6, 1200],
        ],
        1,
      ),
      memory: vary(t, step, 52, 71, [
        [4, 3600],
        [3, 86400],
      ]),
      swap: vary(t, step, 53, 6, [[2, 86400]]),
      'disk:C:\\': vary(t, step, 54, 48, [[0.5, 86400 * 6]]),
      'disk:E:\\': vary(t, step, 55, 64, [[2, 86400 * 10]]),
      'disk.read:C:\\': vary(t, step, 56, 300e3, [[250e3, 30]], 0, 2e9),
      'disk.write:C:\\': vary(t, step, 57, 600e3, [[400e3, 20]], 0, 2e9),
      'disk.read:E:\\': vary(t, step, 58, 4e6 + 20e6 * busy, [[8e6, 120]], 0, 2e9),
      'disk.write:E:\\': vary(t, step, 59, 1e6 + 6e6 * busy, [[3e6, 180]], 0, 2e9),
      'network.receive:Ethernet 1': vary(t, step, 60, 1.5e6 + 6e6 * busy, [[3e6, 60]], 0, 1e9),
      'network.send:Ethernet 1': vary(t, step, 61, 3e6 + 22e6 * busy, [[10e6, 90]], 0, 1e9),
      'network.receive:Ethernet 2': vary(t, step, 62, 800e3, [[600e3, 300]], 0, 1e9),
      'network.send:Ethernet 2': vary(t, step, 63, 300e3, [[250e3, 300]], 0, 1e9),
    };
  },
};

const linuxDesktop: DemoMachine = {
  device: {
    id: 'linux-desktop',
    name: 'Linux desktop',
    address: '192.168.1.22:9393',
    removable: true,
  },
  os: 'linux',
  cores: 12,
  maxClockMHz: 5400,
  memoryBytes: 32 * GB,
  swapBytes: 8 * GB,
  disks: [
    { path: '/', totalBytes: 915 * GB },
    { path: '/home', totalBytes: 1.79 * TB },
  ],
  network: [
    { name: 'enp5s0', addresses: ['192.168.1.22'], linkMbps: 1000 },
    { name: 'wlp4s0' },
    { name: 'docker0', addresses: ['172.17.0.1'] },
  ],
  gpus: [{ name: 'AMD Radeon RX 7800 XT', memoryBytes: 16 * GB }],
  bootedDaysAgo: 1.2,
  values: (t, step) => {
    const busy = workday(t);
    const cpu = vary(
      t,
      step,
      71,
      5 + 25 * busy,
      [
        [15, 45],
        [8, 1200],
      ],
      1,
    );
    const gpu = vary(t, step, 72, 4 + 30 * busy, [
      [25, 2400],
      [8, 60],
    ]);
    return {
      cpu,
      memory: vary(t, step, 73, 34 + 20 * busy, [[5, 1800]]),
      swap: vary(t, step, 74, 2, [[2, 86400]]),
      'temperature:k10temp Tctl': 42 + cpu * 0.4 + vary(t, step, 75, 0, [[2, 120]], -5, 5),
      'temperature:amdgpu edge': 38 + gpu * 0.35,
      'temperature:nvme Composite': 38 + vary(t, step, 76, 0, [[4, 900]], -10, 10),
      'disk:/': vary(t, step, 77, 37, [[0.5, 86400 * 4]]),
      'disk:/home': vary(t, step, 78, 58, [[1, 86400 * 8]]),
      'disk.read:/': vary(t, step, 79, 500e3 + 3e6 * busy, [[2e6, 30]], 0, 3e9),
      'disk.write:/': vary(t, step, 80, 300e3 + 1e6 * busy, [[800e3, 40]], 0, 3e9),
      'disk.read:/home': vary(t, step, 81, 200e3, [[1e6, 200]], 0, 3e9),
      'disk.write:/home': vary(t, step, 82, 100e3, [[400e3, 300]], 0, 3e9),
      'network.receive:enp5s0': vary(t, step, 83, 300e3 + 2e6 * busy, [[1.5e6, 90]], 0, 1.2e8),
      'network.send:enp5s0': vary(t, step, 84, 80e3 + 200e3 * busy, [[150e3, 60]], 0, 1.2e8),
      'network.receive:docker0': vary(t, step, 85, 20e3, [[20e3, 120]], 0, 1e9),
      'network.send:docker0': vary(t, step, 86, 25e3, [[25e3, 120]], 0, 1e9),
      'gpu:AMD Radeon RX 7800 XT': gpu,
      'gpu.memory:AMD Radeon RX 7800 XT': 10 + gpu * 0.45,
    };
  },
};

const linuxNas: DemoMachine = {
  device: { id: 'linux-nas', name: 'Linux NAS', address: '192.168.1.6:9393' },
  os: 'linux',
  cores: 4,
  maxClockMHz: 2900,
  memoryBytes: 16 * GB,
  disks: [
    { path: '/', totalBytes: 56 * GB },
    { path: '/srv/data', totalBytes: 21.8 * TB },
    { path: '/srv/backup', totalBytes: 10.9 * TB },
  ],
  network: [
    { name: 'enp1s0', addresses: ['192.168.1.6'], linkMbps: 2500 },
    { name: 'enp2s0', linkMbps: 2500 },
  ],
  fans: ['nct6798 fan1', 'nct6798 fan2'],
  utc: true,
  bootedDaysAgo: 41.8,
  values: (t, step) => {
    // The backup runs at night and writes for a few hours.
    const backup = Math.max(0, 1 - workday(t) * 3) * Math.max(0, drift(t, 7200, 99));
    const cpu = vary(
      t,
      step,
      91,
      4 + 30 * backup,
      [
        [3, 60],
        [3, 900],
      ],
      1,
    );
    return {
      cpu,
      memory: vary(t, step, 92, 27, [[3, 3600]]),
      'temperature:coretemp Package id 0': 39 + cpu * 0.3,
      'temperature:drivetemp sda': 34 + 4 * backup + vary(t, step, 93, 0, [[1.5, 1800]], -5, 5),
      'temperature:drivetemp sdb': 35 + 4 * backup + vary(t, step, 94, 0, [[1.5, 1800]], -5, 5),
      'disk:/': vary(t, step, 95, 31, [[0.3, 86400 * 5]]),
      'disk:/srv/data': vary(t, step, 96, 71, [[1.5, 86400 * 12]]),
      'disk:/srv/backup': vary(t, step, 97, 83, [[2, 86400 * 6]]),
      'disk.read:/': vary(t, step, 98, 20e3, [[20e3, 60]], 0, 1e9),
      'disk.write:/': vary(t, step, 99, 60e3, [[50e3, 30]], 0, 1e9),
      'disk.read:/srv/data': vary(t, step, 100, 3e6 + 60e6 * backup, [[4e6, 120]], 0, 1e9),
      'disk.write:/srv/data': vary(t, step, 101, 500e3, [[1e6, 300]], 0, 1e9),
      'disk.read:/srv/backup': vary(t, step, 102, 100e3, [[200e3, 300]], 0, 1e9),
      'disk.write:/srv/backup': vary(t, step, 103, 200e3 + 60e6 * backup, [[300e3, 120]], 0, 1e9),
      'network.receive:enp1s0': vary(
        t,
        step,
        104,
        800e3,
        [
          [600e3, 60],
          [400e3, 1800],
        ],
        0,
        3e8,
      ),
      'network.send:enp1s0': vary(
        t,
        step,
        105,
        3e6,
        [
          [3e6, 120],
          [2e6, 1800],
        ],
        0,
        3e8,
      ),
    };
  },
};

const linuxServer: DemoMachine = {
  device: { id: 'linux-server', name: 'Linux build server', address: '192.168.1.7:9393' },
  os: 'linux',
  cores: 32,
  maxClockMHz: 4200,
  memoryBytes: 128 * GB,
  swapBytes: 16 * GB,
  disks: [
    { path: '/', totalBytes: 1.79 * TB },
    { path: '/var/lib/docker', totalBytes: 3.58 * TB },
  ],
  network: [
    { name: 'eno1', addresses: ['192.168.1.7'], linkMbps: 10000 },
    { name: 'eno2' },
    { name: 'docker0', addresses: ['172.17.0.1'] },
  ],
  gpus: [{ name: 'NVIDIA RTX A4000', memoryBytes: 16 * GB }],
  fans: ['nct6799 fan1', 'nct6799 fan2', 'nct6799 fan3'],
  utc: true,
  bootedDaysAgo: 87.4,
  values: (t, step) => {
    // Builds come in bursts of a few minutes.
    const build = Math.max(0, drift(t, 240, 111)) ** 0.7;
    const cpu = vary(t, step, 112, 6 + 80 * build, [[5, 20]], 1);
    const gpu = vary(t, step, 113, 10, [
      [40, 3600],
      [10, 120],
    ]);
    return {
      cpu,
      memory: vary(t, step, 114, 30 + 35 * build, [[4, 600]]),
      swap: vary(t, step, 115, 3, [[2, 86400]]),
      'temperature:k10temp Tctl': 45 + cpu * 0.38,
      'temperature:NVIDIA RTX A4000': 36 + gpu * 0.4,
      'temperature:nvme Composite': 41 + 8 * build,
      'disk:/': vary(t, step, 116, 44, [[0.5, 86400 * 3]]),
      'disk:/var/lib/docker': vary(t, step, 117, 61, [[4, 86400 * 2]]),
      'disk.read:/': vary(t, step, 118, 1e6 + 40e6 * build, [[2e6, 15]], 0, 4e9),
      'disk.write:/': vary(t, step, 119, 500e3 + 25e6 * build, [[1e6, 15]], 0, 4e9),
      'disk.read:/var/lib/docker': vary(t, step, 120, 300e3 + 15e6 * build, [[1e6, 30]], 0, 4e9),
      'disk.write:/var/lib/docker': vary(t, step, 121, 200e3 + 30e6 * build, [[1e6, 30]], 0, 4e9),
      'network.receive:eno1': vary(t, step, 122, 1e6 + 40e6 * build, [[2e6, 30]], 0, 1.2e9),
      'network.send:eno1': vary(t, step, 123, 500e3 + 8e6 * build, [[1e6, 30]], 0, 1.2e9),
      'network.receive:docker0': vary(t, step, 124, 50e3 + 3e6 * build, [[100e3, 60]], 0, 1e9),
      'network.send:docker0': vary(t, step, 125, 80e3 + 5e6 * build, [[100e3, 60]], 0, 1e9),
      'gpu:NVIDIA RTX A4000': gpu,
      'gpu.memory:NVIDIA RTX A4000': 6 + gpu * 0.6,
    };
  },
};

/** The laptop sleeps at night, and went to sleep shortly before the page was opened. */
const laptopOnline = (t: number) => t < openedAt - 47 * 60 && hourOf(t) >= 6;

/** When a device that does not answer now stopped answering, to five minutes. */
export function offlineSince(online: (t: number) => boolean, now: number): number {
  let t = Math.floor(now / 300) * 300;
  while (!online(t - 300) && t > now - 7 * 86400) {
    t -= 300;
  }
  return t;
}

const linuxLaptop: DemoMachine = {
  device: {
    id: 'linux-laptop',
    name: 'Linux laptop',
    address: '192.168.1.38:9393',
    removable: true,
    unreachable: true,
    unreachableSince: new Date(offlineSince(laptopOnline, openedAt) * 1000).toISOString(),
  },
  os: 'linux',
  cores: 8,
  maxClockMHz: 4400,
  memoryBytes: 16 * GB,
  swapBytes: 16 * GB,
  disks: [{ path: '/', totalBytes: 476 * GB }],
  network: [{ name: 'wlp1s0', addresses: ['192.168.1.38'] }],
  batteryDetails: true,
  bootedDaysAgo: 0.3,
  online: laptopOnline,
  values: (t, step) => {
    const cpu = vary(
      t,
      step,
      131,
      12,
      [
        [9, 30],
        [6, 900],
      ],
      1,
    );
    return {
      cpu,
      memory: vary(t, step, 132, 46, [[6, 1200]]),
      swap: vary(t, step, 133, 1, [[1, 86400]]),
      battery: batteryPercent(t + 4000),
      'temperature:coretemp Package id 0': 46 + cpu * 0.45,
      'disk:/': vary(t, step, 134, 68, [[0.5, 86400 * 4]]),
      'disk.read:/': vary(t, step, 135, 400e3, [[350e3, 25]], 0, 2e9),
      'disk.write:/': vary(t, step, 136, 250e3, [[200e3, 25]], 0, 2e9),
      'network.receive:wlp1s0': vary(t, step, 137, 300e3, [[250e3, 60]], 0, 1e9),
      'network.send:wlp1s0': vary(t, step, 138, 50e3, [[40e3, 60]], 0, 1e9),
    };
  },
};

export const FLEET: readonly DemoMachine[] = [
  piHub,
  windowsPc,
  windowsLaptop,
  windowsServer,
  linuxDesktop,
  linuxNas,
  linuxServer,
  linuxLaptop,
];

/** The values whose names start with kind and a colon, by what follows the colon. */
function named(values: Values, kind: string): [string, number][] {
  return Object.entries(values)
    .filter(([metric]) => metric.startsWith(kind + ':'))
    .map(([metric, value]) => [metric.slice(kind.length + 1), value]);
}

/** The visitor's time zone, or UTC for servers, as the backend reports it. */
function timeZone(machine: DemoMachine, t: number): TimeZone {
  if (machine.utc) {
    return { name: 'UTC', offsetSeconds: 0 };
  }
  const date = new Date(t * 1000);
  const offsetSeconds = -date.getTimezoneOffset() * 60;
  const name = new Intl.DateTimeFormat('en-US', { timeZoneName: 'short' })
    .formatToParts(date)
    .find((part) => part.type === 'timeZoneName')?.value;
  // Zones without a short name of their own are named by their offset, like Linux does.
  const hours = Math.trunc(Math.abs(offsetSeconds) / 3600);
  const offsetName = `${offsetSeconds < 0 ? '-' : '+'}${String(hours).padStart(2, '0')}`;
  return { name: name && /^[A-Z]{2,5}$/.test(name) ? name : offsetName, offsetSeconds };
}

/** What GET /api/metrics answers for the machine at t, in Unix seconds. */
export function snapshotOf(machine: DemoMachine, t: number): Snapshot {
  const v = machine.values(t, 0);
  const linux = machine.os === 'linux';
  const uptimeSeconds = Math.round(machine.bootedDaysAgo * 86400 + (t - openedAt));
  const seed = machine.device.id.length * 17;
  const percentOf = (total: number, percent: number) => Math.round((total * percent) / 100);
  // The load average over a window: the usage at a few moments in it, as busy cores.
  const load = (window: number) => {
    const moments = [0, 1, 2, 3, 4, 5].map((i) => machine.values(t - (i * window) / 6, 0)['cpu']);
    return (moments.reduce((sum, cpu) => sum + cpu, 0) / moments.length / 100) * machine.cores;
  };
  const disks = machine.disks.map((disk) => {
    const read = v[`disk.read:${disk.path}`] ?? 0;
    const write = v[`disk.write:${disk.path}`] ?? 0;
    const busyPercent = Math.min(100, (read + write) / 2e6);
    return {
      path: disk.path,
      totalBytes: Math.round(disk.totalBytes),
      usedBytes: percentOf(disk.totalBytes, v[`disk:${disk.path}`]),
      usedPercent: v[`disk:${disk.path}`],
      readBytesPerSecond: read,
      writeBytesPerSecond: write,
      operationsPerSecond: (read + write) / (64 * 1024) + 2,
      ...(linux ? { busyPercent, latencyMs: 0.4 + busyPercent / 40 } : {}),
    };
  });
  const memoryUsed = percentOf(machine.memoryBytes, v['memory']);
  const temperatures = named(v, 'temperature').map(([sensor, celsius]) => ({ sensor, celsius }));
  const hottest = Math.max(45, ...temperatures.map((temp) => temp.celsius));

  return {
    time: new Date(t * 1000).toISOString(),
    timeZone: timeZone(machine, t),
    uptimeSeconds,
    cpu: {
      usagePercent: v['cpu'],
      cores: machine.cores,
      coreUsagePercent: Array.from({ length: machine.cores }, (_, core) =>
        vary(t, 0, seed + core, v['cpu'], [[v['cpu'] * 0.8 + 4, 6]], 0, 100),
      ),
      clockMHz: Math.round(
        machine.maxClockMHz * (0.45 + (0.55 * Math.min(100, v['cpu'] * 3)) / 100),
      ),
      ...(linux
        ? {
            loadAverage: { one: load(60), five: load(300), fifteen: load(900) },
            ioWaitPercent: Math.min(
              20,
              disks.reduce((sum, d) => sum + (d.busyPercent ?? 0), 0) / 20,
            ),
            stealPercent: 0,
            processes: {
              total: Math.round(140 + machine.cores * 12 + v['memory']),
              running: Math.max(1, Math.round((v['cpu'] / 100) * machine.cores)),
            },
          }
        : {}),
    },
    memory: {
      totalBytes: machine.memoryBytes,
      usedBytes: memoryUsed,
      usedPercent: v['memory'],
      availableBytes: machine.memoryBytes - memoryUsed,
      ...(linux ? { cachedBytes: Math.round((machine.memoryBytes - memoryUsed) * 0.6) } : {}),
      ...(machine.swapBytes
        ? {
            swap: {
              totalBytes: machine.swapBytes,
              usedBytes: percentOf(machine.swapBytes, v['swap']),
              usedPercent: v['swap'],
            },
          }
        : {}),
    },
    temperatures,
    disks,
    network: machine.network.map((n) => {
      const receive = v[`network.receive:${n.name}`] ?? 0;
      const send = v[`network.send:${n.name}`] ?? 0;
      return {
        name: n.name,
        // A rough total: the current speed over the time since the machine started.
        receivedBytes: Math.round(receive * uptimeSeconds * 0.8),
        sentBytes: Math.round(send * uptimeSeconds * 0.8),
        receiveBytesPerSecond: receive,
        sendBytesPerSecond: send,
        ...(n.linkMbps ? { linkMbps: n.linkMbps } : {}),
        ...(n.addresses ? { addresses: n.addresses } : {}),
      };
    }),
    gpus: (machine.gpus ?? []).map((gpu) => {
      const usage = v[`gpu:${gpu.name}`];
      return {
        name: gpu.name,
        usagePercent: usage,
        ...(gpu.memoryBytes
          ? {
              memoryTotalBytes: gpu.memoryBytes,
              memoryUsedBytes: percentOf(gpu.memoryBytes, v[`gpu.memory:${gpu.name}`]),
            }
          : {}),
        // Windows reports no GPU temperature.
        ...(linux && gpu.memoryBytes ? { celsius: 36 + usage * 0.4 } : {}),
      };
    }),
    ...(machine.throttling ? { throttling: machine.throttling } : {}),
    ...(v['battery'] !== undefined
      ? {
          battery: {
            percent: v['battery'],
            pluggedIn: charging(t),
            ...(machine.batteryDetails
              ? { watts: charging(t) ? 38 : 7 + v['cpu'] * 0.2, healthPercent: 91 }
              : {}),
          },
        }
      : {}),
    ...(machine.fans
      ? {
          fans: machine.fans.map((name, i) => ({
            name,
            rpm: Math.round(800 + (hottest - 35) * 60 + i * 140),
          })),
        }
      : {}),
  };
}
