import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { FitLabels, MIN_LABEL_SCALE, labelScale } from './fit-labels';

/** Stands in for the browser's ResizeObserver, which jsdom lacks; tests report sizes by hand. */
class FakeResizeObserver {
  static last: FakeResizeObserver | undefined;
  constructor(readonly callback: (entries: { contentRect: { width: number } }[]) => void) {
    FakeResizeObserver.last = this;
  }
  observe = vi.fn();
  disconnect = vi.fn();
  resize(width: number): void {
    this.callback([{ contentRect: { width } }]);
  }
}

describe('labelScale', () => {
  it('keeps the normal size when every name fits', () => {
    expect(labelScale([])).toBe(1);
    expect(
      labelScale([
        { clientWidth: 100, scrollWidth: 100 },
        { clientWidth: 80, scrollWidth: 40 },
      ]),
    ).toBe(1);
  });

  it('shrinks to the size at which the longest name fits', () => {
    expect(
      labelScale([
        { clientWidth: 100, scrollWidth: 100 },
        { clientWidth: 90, scrollWidth: 99 },
        { clientWidth: 95, scrollWidth: 100 },
      ]),
    ).toBe(0.9);
  });

  it('shrinks no further than the smallest readable size', () => {
    expect(labelScale([{ clientWidth: 50, scrollWidth: 200 }])).toBe(MIN_LABEL_SCALE);
  });
});

@Component({
  imports: [FitLabels],
  template: `
    <div [appFitLabels]="names()">
      @for (name of names(); track name) {
        <span class="label">{{ name }}</span>
      }
    </div>
  `,
})
class Host {
  readonly names = signal(['cpu_thermal']);
}

describe('FitLabels', () => {
  /** The width each name would need at the normal size; the labels are 100 pixels wide. */
  const needed: Record<string, number> = {
    cpu_thermal: 60,
    'NVIDIA RTX 4000 Ada Generation': 124,
    'nvme Composite': 90,
  };

  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', FakeResizeObserver);
    vi.stubGlobal('requestAnimationFrame', (callback: () => void) => callback());
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(100);
    vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      return needed[this.textContent.trim()] ?? 0;
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('shrinks all names together when one gets too long, and back when it is gone', () => {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    const card: HTMLElement = fixture.nativeElement.querySelector('div');
    expect(card.style.getPropertyValue('--label-scale')).toBe('');

    fixture.componentInstance.names.set(['cpu_thermal', 'NVIDIA RTX 4000 Ada Generation']);
    fixture.detectChanges();
    expect(card.style.getPropertyValue('--label-scale')).toBe('0.8');

    fixture.componentInstance.names.set(['cpu_thermal', 'nvme Composite']);
    fixture.detectChanges();
    expect(card.style.getPropertyValue('--label-scale')).toBe('');
  });

  it('measures again when the card gets a new width', () => {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.names.set(['NVIDIA RTX 4000 Ada Generation']);
    fixture.detectChanges();
    const card: HTMLElement = fixture.nativeElement.querySelector('div');

    expect(card.style.getPropertyValue('--label-scale')).toBe('0.8');

    // The card got wider: the name now has the 124 pixels it needs.
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(124);
    FakeResizeObserver.last?.resize(400);
    expect(card.style.getPropertyValue('--label-scale')).toBe('');
  });
});
