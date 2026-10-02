import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

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
 * "network.send:eth0" (bytes per second).
 */
export interface Series {
  metric: string;
  points: Point[];
}

export interface Point {
  time: number;
  value: number;
}

/** Reads the machine's usage from the backend. */
@Injectable({ providedIn: 'root' })
export class MetricsService {
  private readonly http = inject(HttpClient);

  current(): Observable<Snapshot> {
    return this.http.get<Snapshot>('/api/metrics');
  }

  /** The usage from `from` to `to`, both Unix seconds. */
  history(from: number, to: number): Observable<History> {
    return this.http.get<History>('/api/history', { params: { from, to } });
  }
}
