import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { Observable, tap } from 'rxjs';

/** A machine whose usage the website shows, as served by GET /api/devices. */
export interface Device {
  /** Picks the device in the API, as ?device=<id>. */
  id: string;
  /** Empty for the machine the backend runs on when its DEVICE_NAME is not set. */
  name: string;
  /** Where another device is reachable, as host:port; missing for the machine the backend runs on. */
  address?: string;
  /** Set for a device added on the page, which can be removed there too. */
  removable?: boolean;
}

/** The body of GET /api/devices. */
export interface DeviceList {
  devices: Device[];
  /** Whether adding or removing a device asks for the password, or chooses it. */
  passwordSet: boolean;
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

/** The shortest password that can be chosen; the backend checks it too. */
export const MIN_PASSWORD_LENGTH = 8;

/**
 * The devices the backend shows and the one picked on the page. The backend
 * collects from other devices on the local network too when they are added on
 * the page or set in its HUB_DEVICES setting.
 */
@Injectable({ providedIn: 'root' })
export class DeviceService {
  private readonly http = inject(HttpClient);

  /** Only this device until the list has been read. */
  readonly devices = signal<Device[]>([LOCAL_DEVICE]);
  readonly passwordSet = signal(false);
  readonly selectedId = signal(LOCAL_DEVICE.id);
  readonly selected = computed(
    () => this.devices().find((d) => d.id === this.selectedId()) ?? LOCAL_DEVICE,
  );

  /** Reads the list of devices from the backend. */
  load(): Observable<DeviceList> {
    return this.http.get<DeviceList>('/api/devices').pipe(
      tap((list) => {
        this.devices.set(list.devices);
        this.passwordSet.set(list.passwordSet);
        if (!list.devices.some((d) => d.id === this.selectedId())) {
          this.selectedId.set(LOCAL_DEVICE.id);
        }
      }),
    );
  }

  /** Adds a device; without a password yet, `password` becomes the password. */
  add(name: string, address: string, password: string): Observable<Device> {
    return this.http.post<Device>('/api/devices', { name, address, password });
  }

  /** Removes a device added on the page, and its history unless `keepHistory` is set. */
  remove(id: string, password: string, keepHistory: boolean): Observable<void> {
    return this.http.delete<void>(`/api/devices/${encodeURIComponent(id)}`, {
      body: { password, keepHistory },
    });
  }
}

/** Explains why adding or removing a device was refused, from the problem the backend names. */
export function problemMessage(error: unknown): string {
  const problem =
    error instanceof HttpErrorResponse
      ? (error.error as { problem?: string } | null)?.problem
      : undefined;
  switch (problem) {
    case 'wrongPassword':
      return $localize`:Error when changing devices@@devices.problem.wrongPassword:The password is wrong.`;
    case 'passwordLength':
      return $localize`:Error when changing devices@@devices.problem.passwordLength:The password needs at least ${MIN_PASSWORD_LENGTH}:count: characters.`;
    case 'name':
      return $localize`:Error when adding a device@@devices.problem.name:Give the device a name of up to 64 characters, with at least one letter or digit.`;
    case 'nameTaken':
      return $localize`:Error when adding a device@@devices.problem.nameTaken:Another device already has this name. Pick another one.`;
    case 'address':
      return $localize`:Error when adding a device@@devices.problem.address:Write the address as IP address and port, such as 192.168.1.20:8080.`;
    case 'unreachable':
      return $localize`:Error when adding a device@@devices.problem.unreachable:No usage-control answers at this address. Is it running, and is the address on the local network?`;
    case 'fixed':
      return $localize`:Error when removing a device@@devices.problem.fixed:This device is set in the hub's .env file. Remove it there.`;
    case 'notFound':
      return $localize`:Error when removing a device@@devices.problem.notFound:This device has been removed already.`;
    default:
      return $localize`:Error when changing devices@@devices.problem.other:The devices could not be changed. Try again.`;
  }
}
