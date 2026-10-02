import { DOCUMENT } from '@angular/common';
import { Component, ElementRef, afterNextRender, inject, viewChild } from '@angular/core';

/**
 * The bear mascot, drawn in the colors of the theme palette.
 *
 * A favicon cannot read the page's CSS, so once the bear is drawn it is also
 * copied, with its colors filled in, into the page's favicon.
 */
@Component({
  selector: 'app-mascot',
  templateUrl: './mascot.html',
  styleUrl: './mascot.css',
})
export class Mascot {
  private readonly document = inject(DOCUMENT);
  private readonly svg = viewChild.required<ElementRef<SVGSVGElement>>('svg');

  constructor() {
    afterNextRender(() => this.updateFavicon());
  }

  private updateFavicon(): void {
    const icon = this.document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (icon) {
      icon.href = svgDataUrl(this.svg().nativeElement);
    }
  }
}

/** Returns a copy of the drawn SVG as a data URL, with its computed colors written into it. */
export function svgDataUrl(svg: SVGSVGElement): string {
  const copy = svg.cloneNode(true) as SVGSVGElement;
  removeAngularAttributes(copy);
  const originals = svg.querySelectorAll<SVGElement>('*');
  copy.querySelectorAll<SVGElement>('*').forEach((shape, i) => {
    const style = getComputedStyle(originals[i]);
    for (const property of ['fill', 'stroke', 'stroke-width', 'stroke-linecap']) {
      shape.setAttribute(property, style.getPropertyValue(property));
    }
    shape.removeAttribute('class');
    removeAngularAttributes(shape);
  });
  copy.removeAttribute('aria-hidden');
  return 'data:image/svg+xml,' + encodeURIComponent(new XMLSerializer().serializeToString(copy));
}

/** Drops the attributes Angular adds for its style scoping, which mean nothing in a favicon. */
function removeAngularAttributes(element: Element): void {
  for (const name of element.getAttributeNames()) {
    if (name.startsWith('_ng')) {
      element.removeAttribute(name);
    }
  }
}
