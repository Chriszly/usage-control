import { provideHttpClient } from '@angular/common/http';
import {
  HttpTestingController,
  TestRequest,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { DEVICES_REFRESH_MS, DevicePicker } from './device-picker';
import { Device, DeviceService } from './devices';

describe('DevicePicker', () => {
  let fixture: ComponentFixture<DevicePicker>;
  let http: HttpTestingController;

  beforeEach(() => {
    vi.useFakeTimers();
    TestBed.configureTestingModule({
      imports: [DevicePicker],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(DevicePicker);
  });

  afterEach(() => {
    http.verify();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  /** The pending request for the list. */
  function request(): TestRequest {
    vi.advanceTimersByTime(0);
    return http.expectOne('/api/devices');
  }

  function respond(devices: Device[]): void {
    request().flush({ devices, passwordSet: false });
    fixture.detectChanges();
  }

  function labels(): string[] {
    const buttons = (fixture.nativeElement as HTMLElement).querySelectorAll('button');
    return Array.from(buttons, (b) => b.textContent?.trim() ?? '');
  }

  it('only offers to add other devices when there are none', () => {
    respond([{ id: 'local', name: '' }]);

    expect(labels()).toEqual(['Add other devices']);
  });

  it('lets the user pick a device once there are others', () => {
    respond([
      { id: 'local', name: '' },
      {
        id: 'living-room-pi',
        name: 'Living room Pi',
        address: '192.168.1.20:9393',
        removable: true,
      },
    ]);

    expect(labels()).toEqual(['Host Hub', 'Living room Pi', 'Devices']);

    (fixture.nativeElement as HTMLElement).querySelectorAll('button')[1].click();

    expect(TestBed.inject(DeviceService).selected().name).toBe('Living room Pi');
  });

  it('marks the picked device and the ones that do not answer', () => {
    respond([
      { id: 'local', name: '' },
      { id: 'pi', name: 'Pi' },
      { id: 'office-pc', name: 'Office PC', unreachable: true },
    ]);

    const buttons = (fixture.nativeElement as HTMLElement).querySelectorAll('.choices button');
    expect(Array.from(buttons, (c) => c.getAttribute('aria-pressed') === 'true')).toEqual([
      true,
      false,
      false,
    ]);
    expect(buttons[1].getAttribute('title')).toBe('Reachable');
    expect(buttons[2].getAttribute('title')).toBe('Not reachable');
    expect(buttons[2].querySelector('.status')?.classList).toContain('unreachable');
    expect(labels()[2]).toBe('Office PC (Not reachable)');
  });

  it('shows a PC that does not answer as not in use rather than unreachable', () => {
    respond([
      { id: 'local', name: '' },
      {
        id: 'laptop',
        name: 'Laptop',
        kind: 'pc',
        unreachable: true,
        unreachableSince: '2026-10-02T11:00:00Z',
      },
    ]);

    const laptop = (fixture.nativeElement as HTMLElement).querySelectorAll('.choices button')[1];
    expect(laptop.getAttribute('title')).toMatch(/^Not in use since /);
    expect(laptop.querySelector('.status')?.classList).toContain('not-in-use');
    expect(laptop.querySelector('.status')?.classList).not.toContain('unreachable');
    expect(labels()[1]).toBe('Laptop (Not in use)');
  });

  it('shows only this device when the list cannot be read', () => {
    request().flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();

    expect(labels()).toEqual(['Add other devices']);
    expect(TestBed.inject(DeviceService).selected().id).toBe('local');
  });

  it('reads the list again every few seconds, but not while the page is hidden', () => {
    let visibilityState: DocumentVisibilityState = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibilityState);
    respond([{ id: 'local', name: '' }]);

    vi.advanceTimersByTime(DEVICES_REFRESH_MS);
    respond([{ id: 'local', name: '' }]);

    visibilityState = 'hidden';
    document.dispatchEvent(new Event('visibilitychange'));
    vi.advanceTimersByTime(10 * DEVICES_REFRESH_MS);
    http.expectNone('/api/devices');

    visibilityState = 'visible';
    document.dispatchEvent(new Event('visibilitychange'));
    respond([
      { id: 'local', name: '' },
      { id: 'pi', name: 'Pi' },
    ]);
    expect(labels()).toEqual(['Host Hub', 'Pi', 'Devices']);
  });
});
