import { Component, computed, inject } from '@angular/core';

import { I18n, LanguageCode } from '../app/i18n/i18n';

/** The notice's text in each language the page is in. */
const TEXT: Record<LanguageCode, { notice: string; install: string }> = {
  'en-GB': {
    notice: 'This is a demo with made-up values from sample devices.',
    install: 'Install Usage Control',
  },
  'en-US': {
    notice: 'This is a demo with made-up values from sample devices.',
    install: 'Install Usage Control',
  },
  de: {
    notice: 'Dies ist eine Demo mit erfundenen Werten von Beispielgeräten.',
    install: 'Usage Control installieren',
  },
  fr: {
    notice: 'Ceci est une démo avec des valeurs inventées d’appareils d’exemple.',
    install: 'Installer Usage Control',
  },
  es: {
    notice: 'Esto es una demo con valores inventados de dispositivos de ejemplo.',
    install: 'Instalar Usage Control',
  },
};

/** Says above the page that its values are made up, and links to the project. */
@Component({
  selector: 'app-demo-notice',
  template: `
    <p>
      {{ text().notice }}
      <a href="https://github.com/Chriszly/usage-control#readme">{{ text().install }}</a>
    </p>
  `,
  styles: `
    p {
      margin: 0;
      padding: 8px 16px;
      text-align: center;
      font: var(--mat-sys-body-medium);
      background: var(--mat-sys-tertiary-container);
      color: var(--mat-sys-on-tertiary-container);
    }
    a {
      color: inherit;
      font-weight: 500;
      margin-left: 8px;
    }
  `,
})
export class DemoNotice {
  private readonly i18n = inject(I18n);
  protected readonly text = computed(() => TEXT[this.i18n.language()]);
}
