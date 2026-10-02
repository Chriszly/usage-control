import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { LOCAL_DEVICE } from '../devices/devices';

/** The usage of the machine at one point in time, as served by GET /api/metrics. */
export interface Snapshot {
  time: string;
  uptimeSeconds: number;
  cpu: {
    usagePercent: number;
    cores: number;
  };
  memory: {
    totalBytes: number;
    usedBytes: number;
    usedPercent: number;
  };
  /** Empty when the machine exposes no temperature sensor. */
  temperatures: Temperature[];
  /** One entry per path in the backend's DISK_PATHS setting. */
  disks: Disk[];
  /** The machine's network cards; empty when it has none. */
  network: NetworkInterface[];
  /** The GPUs whose usage the OS reports; missing from devices that run an older version. */
  gpus?: Gpu[];
}

export interface Temperature {
  sensor: string;
  celsius: number;
}

/** The usage of the filesystem that holds one path. */
export interface Disk {
  path: string;
  totalBytes: number;
  usedBytes: number;
  usedPercent: number;
}

/** The traffic of one network interface, with the speed since the previous request. */
export interface NetworkInterface {
  name: string;
  receivedBytes: number;
  sentBytes: number;
  receiveBytesPerSecond: number;
  sendBytesPerSecond: number;
}

/** The usage of one graphics processor. Memory and temperature are missing where it does not report them. */
export interface Gpu {
  name: string;
  usagePercent: number;
  memoryTotalBytes?: number;
  memoryUsedBytes?: number;
  celsius?: number;
}

/**
 * The machine's usage over a time range, as served by GET /api/history.
 * Times are Unix seconds; each point is the average over one step.
 */
export interface History {
  from: number;
  to: number;
  stepSeconds: number;
  /** How many days of history the backend keeps (its RETENTION_DAYS setting). */
  retentionDays: number;
  series: Series[];
}

/**
 * The values of one metric over time. The metric is "cpu" or "memory" (percent),
 * or a kind followed by the disk, sensor or interface it belongs to: "disk:/"
 * (percent), "temperature:cpu_thermal" (°C), "network.receive:eth0" or
 * "network.send:eth0" (bytes per second), "gpu:AMD GPU" or "gpu.memory:AMD GPU" (percent).
 */
export interface Series {
  metric: string;
  points: Point[];
}

export interface Point {
  time: number;
  value: number;
}

/** Reads the usage of a device from the backend: the one it runs on unless another is named. */
@Injectable({ providedIn: 'root' })
export class MetricsService {
  private readonly http = inject(HttpClient);

  current(device = LOCAL_DEVICE.id): Observable<Snapshot> {
    return this.http.get<Snapshot>('/api/metrics', { params: deviceParam(device) });
  }

  /** The usage from `from` to `to`, both Unix seconds. */
  history(from: number, to: number, device = LOCAL_DEVICE.id): Observable<History> {
    return this.http.get<History>('/api/history', {
      params: { from, to, ...deviceParam(device) },
    });
  }
}

/** The backend answers for the device it runs on when a request names none. */
function deviceParam(device: string): Record<string, string> {
  return device === LOCAL_DEVICE.id ? {} : { device };
}
