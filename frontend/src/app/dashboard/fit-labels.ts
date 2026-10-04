import { DestroyRef, Directive, ElementRef, afterRenderEffect, inject, input } from '@angular/core';

/** The smallest share of the normal size names shrink to; longer ones are cut with an ellipsis. */
export const MIN_LABEL_SCALE = 0.6;

/**
 * The font scale at which the widest of the labels fits, for labels measured at the normal size:
 * 1 when all fit, never below MIN_LABEL_SCALE. Widths are whole pixels, so one is added to stay
 * on the safe side.
 */
export function labelScale(
  labels: readonly { clientWidth: number; scrollWidth: number }[],
): number {
  let scale = 1;
  for (const label of labels) {
    if (label.scrollWidth > label.clientWidth) {
      scale = Math.min(scale, label.clientWidth / (label.scrollWidth + 1));
    }
  }
  return Math.max(MIN_LABEL_SCALE, Math.floor(scale * 100) / 100);
}

/**
 * Shrinks the font of all `.label` elements in the host together until the longest name fits,
 * so the names of one card keep the same size. It measures again when the card's width or what
 * its text needs changes: new names, or values with more or fewer digits, which leave the names
 * less or more room. The digits themselves change with every refresh but, set in tabular figures,
 * keep their width, so measuring for them would only cost layout work.
 */
@Directive({ selector: '[appFitLabels]' })
export class FitLabels {
  /** What the card shows; the labels are measured again whenever it changes. */
  readonly content = input.required<unknown>({ alias: 'appFitLabels' });

  private readonly host: HTMLElement = inject(ElementRef).nativeElement;
  private width = 0;
  /** The card's text with every digit as 0, as of the last fit. */
  private shape: string | null = null;

  constructor() {
    afterRenderEffect(() => {
      this.content();
      const shape = (this.host.textContent ?? '').replace(/\d/g, '0');
      if (shape !== this.shape) {
        this.shape = shape;
        this.fit();
      }
    });

    // Of the card's sizes only its width changes what fits; the fit is applied in the next frame, so the
    // observer does not report a size it caused itself.
    const observer = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width !== this.width) {
        this.width = entry.contentRect.width;
        requestAnimationFrame(() => this.fit());
      }
    });
    observer.observe(this.host);
    inject(DestroyRef).onDestroy(() => observer.disconnect());
  }

  private fit(): void {
    this.host.style.removeProperty('--label-scale');
    const scale = labelScale([...this.host.querySelectorAll<HTMLElement>('.label')]);
    if (scale < 1) {
      this.host.style.setProperty('--label-scale', String(scale));
    }
  }
}
