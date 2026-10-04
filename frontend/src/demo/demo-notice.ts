import { Component, computed, inject } from '@angular/core';

import { I18n, LanguageCode } from '../app/i18n/i18n';
import { DEMO_BUILD } from './demo-build';

interface NoticeText {
  notice: string;
  install: string;
  /** Names the release the demo shows; {version} is its version. */
  release: string;
  /** Names a demo of main; {commit} is the commit it was built from. */
  main: string;
  toMain: string;
  toRelease: string;
}

const ENGLISH: NoticeText = {
  notice: 'This is a demo with made-up values from sample devices.',
  install: 'Install Usage Control',
  release: 'Version {version}',
  main: 'Development version from main {commit}',
  toMain: 'See the development version',
  toRelease: 'See the latest release',
};

/** The notice's text in each language the page is in. */
const TEXT: Record<LanguageCode, NoticeText> = {
  'en-GB': ENGLISH,
  'en-US': ENGLISH,
  de: {
    notice: 'Dies ist eine Demo mit erfundenen Werten von Beispielgeräten.',
    install: 'Usage Control installieren',
    release: 'Version {version}',
    main: 'Entwicklungsstand von main {commit}',
    toMain: 'Entwicklungsstand ansehen',
    toRelease: 'Neueste Version ansehen',
  },
  fr: {
    notice: 'Ceci est une démo avec des valeurs inventées d’appareils d’exemple.',
    install: 'Installer Usage Control',
    release: 'Version {version}',
    main: 'Version de développement de main {commit}',
    toMain: 'Voir la version de développement',
    toRelease: 'Voir la dernière version',
  },
  es: {
    notice: 'Esto es una demo con valores inventados de dispositivos de ejemplo.',
    install: 'Instalar Usage Control',
    release: 'Versión {version}',
    main: 'Versión en desarrollo de main {commit}',
    toMain: 'Ver la versión en desarrollo',
    toRelease: 'Ver la última versión',
  },
};

/**
 * Says above the page that its values are made up, which version it shows,
 * and links to the demo of the other version and to the project. The latest
 * release is at the root of the site and main under main/, so the links are
 * relative to the page's base address.
 */
@Component({
  selector: 'app-demo-notice',
  template: `
    <p>
      {{ text().notice }}
      <span class="version">{{ version() }}</span>
      <a [href]="build.release ? 'main/' : '../'">{{
        build.release ? text().toMain : text().toRelease
      }}</a>
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
    .version {
      margin-left: 8px;
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
  protected readonly build = inject(DEMO_BUILD);
  protected readonly text = computed(() => TEXT[this.i18n.language()]);
  protected readonly version = computed(() =>
    this.build.release
      ? this.text().release.replace('{version}', this.build.release)
      : this.text()
          .main.replace('{commit}', this.build.commit ?? '')
          .trim(),
  );
}
