import { DOCUMENT } from '@angular/common';
import { DestroyRef, Injectable, computed, inject, signal } from '@angular/core';

export type ColorScheme = 'light' | 'dark';

/**
 * Follows the color scheme of the operating system until the user picks one.
 * The choice is not stored, so every page load starts from the system again.
 */
@Injectable({ providedIn: 'root' })
export class ThemeService {
  private readonly document = inject(DOCUMENT);
  private readonly systemQuery = this.document.defaultView?.matchMedia?.(
    '(prefers-color-scheme: dark)',
  );

  private readonly system = signal<ColorScheme>(this.systemQuery?.matches ? 'dark' : 'light');
  private readonly chosen = signal<ColorScheme | null>(null);

  /** The color scheme the page is shown in right now. */
  readonly scheme = computed(() => this.chosen() ?? this.system());

  constructor() {
    const query = this.systemQuery;
    if (query) {
      const update = (event: MediaQueryListEvent) =>
        this.system.set(event.matches ? 'dark' : 'light');
      query.addEventListener('change', update);
      inject(DestroyRef).onDestroy(() => query.removeEventListener('change', update));
    }
  }

  /** Switches between light and dark. */
  toggle(): void {
    const next: ColorScheme = this.scheme() === 'dark' ? 'light' : 'dark';
    this.chosen.set(next);
    this.document.documentElement.style.colorScheme = next;
  }
}
