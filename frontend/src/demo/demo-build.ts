import { InjectionToken } from '@angular/core';

/**
 * Which code a demo was built from. The Demo page workflow builds the latest
 * release at the root of the site and main under main/, and passes these with
 * `ng build --define`; a build without them, such as a local one, counts as main.
 */
declare const DEMO_RELEASE: string | undefined;
declare const DEMO_COMMIT: string | undefined;

export interface DemoBuild {
  /** The release's version, such as "1.2.0"; missing for a build of main. */
  release?: string;
  /** The short commit of main the demo was built from, when known. */
  commit?: string;
}

export const DEMO_BUILD = new InjectionToken<DemoBuild>('demo build', {
  factory: () => ({
    release: typeof DEMO_RELEASE === 'string' ? DEMO_RELEASE : undefined,
    commit: typeof DEMO_COMMIT === 'string' ? DEMO_COMMIT : undefined,
  }),
});
