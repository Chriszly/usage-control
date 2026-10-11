import {
  Component,
  DestroyRef,
  ElementRef,
  afterEveryRender,
  inject,
  signal,
  viewChild,
} from '@angular/core';

import { I18n } from '../i18n/i18n';

/** The most entries a card shows at once; more scroll. */
export const MAX_ROWS = 8;

/**
 * How high the list may be to show its first `max` rows, from their offsets in the list: the bottom
 * of the last one shown. Null while all rows fit, so the list keeps its natural height.
 */
export function rowsHeight(
  rows: readonly { offsetTop: number; offsetHeight: number }[],
  max = MAX_ROWS,
): number | null {
  if (rows.length <= max) {
    return null;
  }
  const last = rows[max - 1];
  return last.offsetTop + last.offsetHeight;
}

/** Whether rows are hidden below the visible part of a list; a pixel is left for rounding. */
export function moreBelow(list: {
  scrollTop: number;
  clientHeight: number;
  scrollHeight: number;
}): boolean {
  return list.scrollTop + list.clientHeight < list.scrollHeight - 1;
}

/**
 * The entries of a card, of which it shows the first eight; more scroll, without a scrollbar. An
 * arrow below them tells that more follow and scrolls down a page; it hides at the bottom. It
 * measures again after every render, as values that refresh can change how high a row is, and when
 * its width changes, which can wrap rows.
 */
@Component({
  selector: 'app-scroll-rows',
  templateUrl: './scroll-rows.html',
  styleUrl: './scroll-rows.css',
})
export class ScrollRows {
  protected readonly i18n = inject(I18n);
  /** Whether the entries are more than fit, so the list scrolls. */
  protected readonly scrolls = signal(false);
  /** Whether entries are hidden below, so the arrow shows. */
  protected readonly more = signal(false);

  private readonly list = viewChild.required<ElementRef<HTMLElement>>('list');
  private width = 0;

  constructor() {
    afterEveryRender(() => this.measure());

    const host: HTMLElement = inject(ElementRef).nativeElement;
    const observer = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width !== this.width) {
        this.width = entry.contentRect.width;
        requestAnimationFrame(() => this.measure());
      }
    });
    observer.observe(host);
    inject(DestroyRef).onDestroy(() => observer.disconnect());
  }

  protected update(): void {
    this.more.set(this.scrolls() && moreBelow(this.list().nativeElement));
  }

  protected scrollDown(): void {
    const list = this.list().nativeElement;
    list.scrollBy({ top: list.clientHeight, behavior: 'smooth' });
  }

  private measure(): void {
    const list = this.list().nativeElement;
    const height = rowsHeight([...list.children] as HTMLElement[]);
    const maxHeight = height === null ? '' : `${height}px`;
    if (list.style.maxHeight !== maxHeight) {
      list.style.maxHeight = maxHeight;
    }
    this.scrolls.set(height !== null);
    this.update();
  }
}
