import { TestBed } from '@angular/core/testing';

import { Mascot, svgDataUrl } from './mascot';

describe('Mascot', () => {
  let icon: HTMLLinkElement;

  beforeEach(() => {
    icon = document.createElement('link');
    icon.rel = 'icon';
    icon.href = 'mascot.svg';
    document.head.appendChild(icon);
  });

  afterEach(() => icon.remove());

  it('uses the drawn bear as the favicon', async () => {
    const fixture = TestBed.createComponent(Mascot);
    await fixture.whenStable();

    expect(icon.href).toMatch(/^data:image\/svg\+xml,/);
    expect(decodeURIComponent(icon.href)).not.toContain('_ng');
  });

  it('writes the computed colors into the favicon copy', () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    const shape = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    shape.setAttribute('class', 'fur');
    shape.style.fill = 'rgb(227, 113, 0)';
    svg.appendChild(shape);
    document.body.appendChild(svg);

    const copy = decodeURIComponent(svgDataUrl(svg).split(',')[1]);
    svg.remove();

    expect(copy).toContain('fill="rgb(227, 113, 0)"');
    expect(copy).not.toContain('class=');
  });
});
