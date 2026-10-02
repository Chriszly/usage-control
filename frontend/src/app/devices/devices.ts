import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { Observable } from 'rxjs';

/** A machine whose usage the website shows, as served by GET /api/devices. */
export interface Device {
  /** Picks the device in the API, as ?device=<id>. */
  id: string;
  /** Empty for the machine the backend runs on when its DEVICE_NAME is not set. */
  name: string;
}

/** The machine the backend runs on. The API answers for it when a request names no device. */
export const LOCAL_DEVICE: Device = { id: 'local', name: '' };

/** How the page names a device. */
export function deviceName(device: Device): string {
  return (
    device.name ||
    $localize`:Name of the device the website runs on@@devices.thisDevice:This device`
  );
}

/**
 * The devices the backend shows and the one picked on the page. In hub mode
 * the backend collects from other devices on the local network too.
 */
@Injectable({ providedIn: 'root' })
export class DeviceService {
  private readonly http = inject(HttpClient);

  /** Only this device until the list has been read. */
  readonly devices = signal<Device[]>([LOCAL_DEVICE]);
  readonly selectedId = signal(LOCAL_DEVICE.id);
  readonly selected = computed(
    () => this.devices().find((d) => d.id === this.selectedId()) ?? LOCAL_DEVICE,
  );

  /** Reads the list of devices from the backend. */
  load(): Observable<Device[]> {
    return this.http.get<Device[]>('/api/devices');
  }
}
