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
    http.expectOne('/api/devices').flush(devices);
    fixture.detectChanges();
  }

  function buttons(): HTMLButtonElement[] {
    return Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button'));
  }

  it('shows nothing when the backend only shows its own device', () => {
    respond([{ id: 'local', name: '' }]);

    expect(buttons()).toHaveLength(0);
  });

  it('lets the user pick a device in hub mode', () => {
    respond([
      { id: 'local', name: '' },
      { id: 'living-room-pi', name: 'Living room Pi' },
    ]);

    expect(buttons().map((b) => b.textContent?.trim())).toEqual(['This device', 'Living room Pi']);

    buttons()[1].click();

    expect(TestBed.inject(DeviceService).selected().name).toBe('Living room Pi');
  });

  it('shows only this device when the list cannot be read', () => {
    http.expectOne('/api/devices').flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();

    expect(buttons()).toHaveLength(0);
    expect(TestBed.inject(DeviceService).selected().id).toBe('local');
  });
});
