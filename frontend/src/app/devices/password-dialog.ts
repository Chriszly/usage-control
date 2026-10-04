import { Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import {
  MAT_DIALOG_DATA,
  MatDialogActions,
  MatDialogClose,
  MatDialogContent,
  MatDialogRef,
  MatDialogTitle,
} from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';

import { I18n } from '../i18n/i18n';
import { DeviceKind, MIN_PASSWORD_LENGTH, kindName } from './devices';

/** What the password is asked for. */
export interface PasswordDialogData {
  action: 'add' | 'remove' | 'kind';
  /** The name of the device that is changed. */
  deviceName: string;
  /** For changing the kind: the new kind. */
  kind?: DeviceKind;
  /** Whether the password has been chosen; if not, this change chooses it. */
  passwordSet: boolean;
}

/** What the visitor confirmed. */
export interface PasswordDialogResult {
  password: string;
  /** For removing: keep the device's history. */
  keepHistory: boolean;
}

/**
 * Asks for the password before a device is added, removed or changed. Before the first
 * change it asks to choose one instead, twice, since it cannot be changed later.
 */
@Component({
  selector: 'app-password-dialog',
  imports: [
    FormsModule,
    MatButtonModule,
    MatCheckboxModule,
    MatDialogActions,
    MatDialogClose,
    MatDialogContent,
    MatDialogTitle,
    MatFormFieldModule,
    MatInputModule,
  ],
  templateUrl: './password-dialog.html',
  styleUrl: './password-dialog.css',
})
export class PasswordDialog {
  protected readonly data = inject<PasswordDialogData>(MAT_DIALOG_DATA);
  protected readonly i18n = inject(I18n);
  private readonly dialog =
    inject<MatDialogRef<PasswordDialog, PasswordDialogResult>>(MatDialogRef);

  protected readonly minLength = MIN_PASSWORD_LENGTH;
  protected readonly password = signal('');
  protected readonly repeated = signal('');
  protected readonly keepHistory = signal(false);

  protected readonly tooShort = computed(
    () => !this.data.passwordSet && this.password().length < MIN_PASSWORD_LENGTH,
  );
  protected readonly mismatch = computed(
    () => !this.data.passwordSet && this.repeated() !== '' && this.repeated() !== this.password(),
  );
  protected readonly canConfirm = computed(() =>
    this.data.passwordSet
      ? this.password() !== ''
      : !this.tooShort() && this.repeated() === this.password(),
  );

  /** What the password is asked for, as a question to confirm. */
  protected readonly question = computed(() => {
    const device = this.data.deviceName;
    switch (this.data.action) {
      case 'add':
        return this.i18n.t('passwordDialog.add', { device });
      case 'remove':
        return this.i18n.t('passwordDialog.remove', { device });
      case 'kind':
        return this.i18n.t('passwordDialog.kind', {
          device,
          kind: kindName(this.data.kind ?? 'server', this.i18n),
        });
    }
  });

  protected readonly confirmLabel = computed(() => {
    switch (this.data.action) {
      case 'add':
        return this.i18n.t('passwordDialog.confirmAdd');
      case 'remove':
        return this.i18n.t('passwordDialog.confirmRemove');
      case 'kind':
        return this.i18n.t('passwordDialog.confirmKind');
    }
  });

  protected confirm(): void {
    if (this.canConfirm()) {
      this.dialog.close({ password: this.password(), keepHistory: this.keepHistory() });
    }
  }
}
