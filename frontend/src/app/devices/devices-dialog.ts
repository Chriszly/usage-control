import { Component, Injector, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
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
import {
  EMPTY,
  Observable,
  catchError,
  filter,
  finalize,
  ignoreElements,
  of,
  switchMap,
  tap,
} from 'rxjs';

import { I18n } from '../i18n/i18n';
import {
  DEVICE_KINDS,
  Device,
  DeviceKind,
  DeviceService,
  Suggestion,
  kindName,
  problemMessage,
} from './devices';
import { PasswordDialog, PasswordDialogData, PasswordDialogResult } from './password-dialog';

/**
 * Lists the other devices the hub collects from, adds or removes them and
 * changes what they are used as. Every change asks for the password in a
 * dialog of its own.
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
  protected readonly kind = signal<DeviceKind>('server');
  protected readonly kinds = DEVICE_KINDS;
  protected readonly problem = signal('');
  protected readonly busy = signal(false);
  /** The device the page is open on, as the hub suggested it. */
  private readonly suggestion = signal<Suggestion | null>(null);
  /** Whether the form holds the suggested device. */
  protected readonly suggested = computed(
    () => this.suggestion()?.address === this.address().trim(),
  );

  constructor() {
    // Filled in only when nothing has been typed yet; a suggestion that
    // cannot be read leaves the form empty, as before.
    this.devices
      .suggestion()
      .pipe(
        catchError(() => of(null)),
        takeUntilDestroyed(),
      )
      .subscribe((suggestion) => {
        if (!suggestion || this.name() || this.address()) {
          return;
        }
        this.suggestion.set(suggestion);
        this.name.set(suggestion.name);
        this.address.set(suggestion.address);
        this.kind.set(suggestion.kind);
      });
  }

  protected add(): void {
    const name = this.name().trim();
    const address = this.address().trim();
    if (!name || !address) {
      return;
    }
    const kind = this.kind();
    this.change({ action: 'add', deviceName: name }, (result) =>
      this.devices.add(name, address, kind, result.password),
    ).subscribe(() => {
      this.name.set('');
      this.address.set('');
      this.kind.set('server');
    });
  }

  protected kindName(kind: DeviceKind): string {
    return kindName(kind, this.i18n);
  }

  protected setKind(device: Device, kind: DeviceKind): void {
    if (device.kind === kind) {
      return;
    }
    this.change({ action: 'kind', deviceName: device.name, kind }, (result) =>
      this.devices.setKind(device.id, kind, result.password),
    ).subscribe();
  }

  protected remove(device: Device): void {
    this.change({ action: 'remove', deviceName: device.name }, (result) =>
      this.devices.remove(device.id, result.password, result.keepHistory),
    ).subscribe();
  }

  /**
   * Asks for the password, makes the change and reads the list again. Emits
   * once when the change worked; a refused change shows its problem instead,
   * and the list is read again too, as a change that got no answer may still
   * have been made. The buttons work again right away rather than after that
   * read, which can take as long again when the hub is gone.
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
        // Once the change worked, a list that cannot be read is no failure of
        // the change: the device picker reads the list again in a few seconds.
        switchMap((result) =>
          makeChange(result).pipe(
            switchMap(() => this.devices.load().pipe(catchError(() => of(null)))),
          ),
        ),
        catchError((error: unknown) => {
          this.problem.set(problemMessage(error, this.i18n));
          this.busy.set(false);
          return this.devices.load().pipe(
            ignoreElements(),
            catchError(() => EMPTY),
          );
        }),
        finalize(() => this.busy.set(false)),
      );
  }
}

/** Opens the list of devices. */
export function openDevicesDialog(injector: Injector): void {
  injector.get(MatDialog).open(DevicesDialog, { autoFocus: 'first-tabbable', injector });
}
