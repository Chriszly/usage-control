import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { Observable, tap } from 'rxjs';

import { I18n } from '../i18n/i18n';

/**
 * What another device is used as. The times a server does not answer are
 * outages; a PC or laptop that does not answer is switched off or asleep, so
 * it is just not in use.
 */
export type DeviceKind = 'server' | 'pc';

/** The kinds in the order the page offers them. */
export const DEVICE_KINDS: readonly DeviceKind[] = ['server', 'pc'];

/** A machine whose usage the website shows, as served by GET /api/devices. */
export interface Device {
  /** Picks the device in the API, as ?device=<id>. */
  id: string;
  /** Empty for the machine the backend runs on when its DEVICE_NAME is not set. */
  name: string;
  /** Where another device is reachable, as host:port; missing for the machine the backend runs on. */
  address?: string;
  /** What another device is used as; missing for the machine the backend runs on. */
  kind?: DeviceKind;
  /** Set for a device added on the page, which can be removed there too. */
  removable?: boolean;
  /** Set for another device that has not answered the backend recently. */
  unreachable?: boolean;
  /** When it stopped answering, as an ISO time, when that is known. */
  unreachableSince?: string;
}

/** The body of GET /api/availability: how long another device did not answer since it was added. */
export interface Availability {
  /** Whether the times it did not answer are outages of a server or times a PC was not in use. */
  kind: DeviceKind;
  /** When the device was added, as an ISO time. */
  since: string;
  /** All outages added up; time the backend itself was not running is not counted. */
  offlineSeconds: number;
  outages: number;
  /** The newest outage; its end is the newest failed reading while it lasts. */
  lastOutage?: { start: string; end: string };
}

/** The body of GET /api/devices. */
export interface DeviceList {
  devices: Device[];
  /** Whether adding or removing a device asks for the password, or chooses it. */
  passwordSet: boolean;
}

/**
 * The body of GET /api/devices/suggestion: the device the page is open on,
 * which the hub offers to add when it does not collect from it yet.
 */
export interface Suggestion {
  /** Where its usage-control would be reachable, as host:port. */
  address: string;
  /** What the device calls itself, or its name in the local DNS; empty when neither is known. */
  name: string;
  /** What the device seems to be: a PC when it has a battery or runs Windows. */
  kind: DeviceKind;
}

/** The machine the backend runs on. The API answers for it when a request names no device. */
export const LOCAL_DEVICE: Device = { id: 'local', name: '' };

/** How the page names a device, in the page's language. */
export function deviceName(device: Device, i18n: I18n): string {
  return device.name || i18n.t('devices.hostHub');
}

/** How the page names the hub, the machine the backend runs on, from the list of devices. */
export function hubName(devices: Device[], i18n: I18n): string {
  return deviceName(devices.find((d) => d.id === LOCAL_DEVICE.id) ?? LOCAL_DEVICE, i18n);
}

/** How the page names a kind of device, in the page's language. */
export function kindName(kind: DeviceKind, i18n: I18n): string {
  return i18n.t(`devices.kind.${kind}`);
}

/** Whether a device that does not answer is just not in use rather than having an outage. */
export function notInUse(device: Device): boolean {
  return device.kind === 'pc' && !!device.unreachable;
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
  add(name: string, address: string, kind: DeviceKind, password: string): Observable<Device> {
    return this.http.post<Device>('/api/devices', { name, address, kind, password });
  }

  /** Changes what a device is used as, one from the hub's .env file too. */
  setKind(id: string, kind: DeviceKind, password: string): Observable<void> {
    return this.http.put<void>(`/api/devices/${encodeURIComponent(id)}/kind`, { kind, password });
  }

  /**
   * Asks the hub which device the page is open on, to offer adding it. Emits
   * null when there is none to offer, as when it is added already.
   */
  suggestion(): Observable<Suggestion | null> {
    return this.http.get<Suggestion | null>('/api/devices/suggestion');
  }

  /** Reads how long another device did not answer since it was added. */
  availability(id: string): Observable<Availability> {
    return this.http.get<Availability>('/api/availability', { params: { device: id } });
  }

  /** Removes a device added on the page, and its history unless `keepHistory` is set. */
  remove(id: string, password: string, keepHistory: boolean): Observable<void> {
    return this.http.delete<void>(`/api/devices/${encodeURIComponent(id)}`, {
      body: { password, keepHistory },
    });
  }
}

/** The problems the backend names that have a message of their own, besides passwordLength. */
const KNOWN_PROBLEMS = [
  'wrongPassword',
  'name',
  'nameTaken',
  'addressTaken',
  'address',
  'kind',
  'unreachable',
  'fixed',
  'notFound',
  'removing',
  // Only the demo on GitHub Pages refuses every change with this.
  'demo',
] as const;

/** Explains why adding or removing a device was refused, from the problem the backend names. */
export function problemMessage(error: unknown, i18n: I18n): string {
  const problem =
    error instanceof HttpErrorResponse
      ? (error.error as { problem?: string } | null)?.problem
      : undefined;
  if (problem === 'passwordLength') {
    return i18n.t('devices.problem.passwordLength', { count: MIN_PASSWORD_LENGTH });
  }
  const known = KNOWN_PROBLEMS.find((p) => p === problem);
  return i18n.t(known ? `devices.problem.${known}` : 'devices.problem.other');
}
