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

/** Reads the machine's usage from the backend. */
@Injectable({ providedIn: 'root' })
export class MetricsService {
  private readonly http = inject(HttpClient);

  current(): Observable<Snapshot> {
    return this.http.get<Snapshot>('/api/metrics');
  }
}
