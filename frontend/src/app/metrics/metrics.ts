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
}

export interface Temperature {
  sensor: string;
  celsius: number;
}

/** Reads the machine's usage from the backend. */
@Injectable({ providedIn: 'root' })
export class MetricsService {
  private readonly http = inject(HttpClient);

  current(): Observable<Snapshot> {
    return this.http.get<Snapshot>('/api/metrics');
  }
}
