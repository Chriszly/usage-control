import { Component, Injector, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { EMPTY, catchError } from 'rxjs';

import { DeviceService, deviceName } from './devices';

/**
 * Buttons to pick the device whose usage is shown, once the hub collects from
 * other devices, and a button that opens the list of devices to add or remove one.
 */
@Component({
  selector: 'app-device-picker',
  imports: [MatButtonModule, MatButtonToggleModule],
  templateUrl: './device-picker.html',
  styleUrl: './device-picker.css',
})
export class DevicePicker {
  protected readonly devices = inject(DeviceService);
  private readonly injector = inject(Injector);
  protected readonly deviceName = deviceName;

  constructor() {
    // Without the list only this device is shown.
    this.devices
      .load()
      .pipe(
        catchError(() => EMPTY),
        takeUntilDestroyed(),
      )
      .subscribe();
  }

  /** The dialogs are loaded only when they are opened, so the page loads fast. */
  protected async openDevices(): Promise<void> {
    const { openDevicesDialog } = await import('./devices-dialog');
    openDevicesDialog(this.injector);
  }
}
