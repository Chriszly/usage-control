import { DestroyRef, Directive, ElementRef, afterNextRender, inject } from '@angular/core';

/** The narrowest a card gets, in rem; the same as the `minmax()` of `.cards` in dashboard.css. */
export const MIN_CARD_REM = 14;

/**
 * How many columns `cards` cards get in a grid `width` pixels wide: as many as fit, but no more
 * than keep the rows even, so a card never wraps onto a row of its own (7 that fit 6 per row
 * become 4 + 3, 8 that fit 7 become 4 + 4).
 */
export function evenColumns(cards: number, width: number, minCard: number, gap: number): number {
  const fit = Math.max(1, Math.floor((width + gap) / (minCard + gap)));
  const rows = Math.max(1, Math.ceil(cards / fit));
  return Math.max(1, Math.ceil(cards / rows));
}

/**
 * Sets `--columns` on a grid of cards to the count that keeps its rows even. It counts again when
 * cards come or go (some only show when the device reports them) and when the grid's width changes.
 */
@Directive({ selector: '[appEvenColumns]' })
export class EvenColumns {
  private readonly host: HTMLElement = inject(ElementRef).nativeElement;
  private width = 0;

  constructor() {
    afterNextRender(() => this.update());

    const cards = new MutationObserver(() => this.update());
    cards.observe(this.host, { childList: true });

    // Only the width changes how many cards fit; the columns never change the grid's width.
    const size = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width !== this.width) {
        this.width = entry.contentRect.width;
        this.update();
      }
    });
    size.observe(this.host);

    inject(DestroyRef).onDestroy(() => {
      cards.disconnect();
      size.disconnect();
    });
  }

  private update(): void {
    const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
    const gap = parseFloat(getComputedStyle(this.host).columnGap) || 0;
    const columns = evenColumns(
      this.host.children.length,
      this.host.getBoundingClientRect().width,
      MIN_CARD_REM * rem,
      gap,
    );
    this.host.style.setProperty('--columns', String(columns));
  }
}
