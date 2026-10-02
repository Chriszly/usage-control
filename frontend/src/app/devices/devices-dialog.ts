import { Component, Injector, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import {
  MatDialog,
  MatDialogActions,
  MatDialogClose,
  MatDialogContent,
  MatDialogTitle,
} from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { EMPTY, Observable, catchError, filter, finalize, switchMap, tap } from 'rxjs';

import { I18n } from '../i18n/i18n';
import { Device, DeviceService, problemMessage } from './devices';
import { PasswordDialog, PasswordDialogData, PasswordDialogResult } from './password-dialog';

/**
 * Lists the other devices the hub collects from and adds or removes them.
 * Every change asks for the password in a dialog of its own.
 */
@Component({
  selector: 'app-devices-dialog',
  imports: [
    FormsModule,
    MatButtonModule,
    MatDialogActions,
    MatDialogClose,
    MatDialogContent,
    MatDialogTitle,
    MatFormFieldModule,
    MatInputModule,
  ],
  templateUrl: './devices-dialog.html',
  styleUrl: './devices-dialog.css',
})
export class DevicesDialog {
  protected readonly i18n = inject(I18n);
  private readonly devices = inject(DeviceService);
  private readonly dialog = inject(MatDialog);

  /** Every device but the one the website runs on. */
  protected readonly others = computed(() => this.devices.devices().slice(1));

  protected readonly name = signal('');
  protected readonly address = signal('');
  protected readonly problem = signal('');
  protected readonly busy = signal(false);

  protected add(): void {
    const name = this.name().trim();
    const address = this.address().trim();
    if (!name || !address) {
      return;
    }
    this.change({ action: 'add', deviceName: name }, (result) =>
      this.devices.add(name, address, result.password),
    ).subscribe(() => {
      this.name.set('');
      this.address.set('');
    });
  }

  protected remove(device: Device): void {
    this.change({ action: 'remove', deviceName: device.name }, (result) =>
      this.devices.remove(device.id, result.password, result.keepHistory),
    ).subscribe();
  }

  /**
   * Asks for the password, makes the change and reads the list again. Emits
   * once when the change worked; a refused change shows its problem instead.
   */
  private change(
    ask: Omit<PasswordDialogData, 'passwordSet'>,
    makeChange: (result: PasswordDialogResult) => Observable<unknown>,
  ): Observable<unknown> {
    const data: PasswordDialogData = { ...ask, passwordSet: this.devices.passwordSet() };
    return this.dialog
      .open<PasswordDialog, PasswordDialogData, PasswordDialogResult>(PasswordDialog, { data })
      .afterClosed()
      .pipe(
        filter((result) => result !== undefined),
        tap(() => {
          this.busy.set(true);
          this.problem.set('');
        }),
        switchMap((result) => makeChange(result).pipe(switchMap(() => this.devices.load()))),
        catchError((error: unknown) => {
          this.problem.set(problemMessage(error, this.i18n));
          return EMPTY;
        }),
        finalize(() => this.busy.set(false)),
      );
  }
}

/** Opens the list of devices. */
export function openDevicesDialog(injector: Injector): void {
  injector.get(MatDialog).open(DevicesDialog, { autoFocus: 'first-tabbable', injector });
}
