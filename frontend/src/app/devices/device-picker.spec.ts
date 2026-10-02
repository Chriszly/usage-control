import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { DevicePicker } from './device-picker';
import { Device, DeviceService } from './devices';

describe('DevicePicker', () => {
  let fixture: ComponentFixture<DevicePicker>;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [DevicePicker],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(DevicePicker);
  });

  afterEach(() => http.verify());

  function respond(devices: Device[]): void {
    http.expectOne('/api/devices').flush({ devices, passwordSet: false });
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
        address: '192.168.1.20:8080',
        removable: true,
      },
    ]);

    expect(labels()).toEqual(['This device', 'Living room Pi', 'Devices']);

    (fixture.nativeElement as HTMLElement).querySelectorAll('button')[1].click();

    expect(TestBed.inject(DeviceService).selected().name).toBe('Living room Pi');
  });

  it('marks the picked device and the ones that do not answer', () => {
    respond([
      { id: 'local', name: '' },
      { id: 'pi', name: 'Pi' },
      { id: 'office-pc', name: 'Office PC', unreachable: true },
    ]);

    const buttons = (fixture.nativeElement as HTMLElement).querySelectorAll('button');
    expect(Array.from(buttons, (b) => b.getAttribute('aria-pressed'))).toEqual([
      'true',
      'false',
      'false',
      null,
    ]);
    expect(buttons[1].title).toBe('Reachable');
    expect(buttons[2].title).toBe('Not reachable');
    expect(buttons[2].querySelector('.status')?.classList).toContain('unreachable');
    expect(labels()[2]).toBe('Office PC (Not reachable)');
  });

  it('shows only this device when the list cannot be read', () => {
    http.expectOne('/api/devices').flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();

    expect(labels()).toEqual(['Add other devices']);
    expect(TestBed.inject(DeviceService).selected().id).toBe('local');
  });
});
