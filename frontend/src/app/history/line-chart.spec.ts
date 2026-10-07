import { ComponentFixture, TestBed } from '@angular/core/testing';

import { ChartLine, LineChart, niceCeiling } from './line-chart';

describe('LineChart', () => {
  let fixture: ComponentFixture<LineChart>;

  function render(lines: ChartLine[]): HTMLElement {
    fixture = TestBed.createComponent(LineChart);
    fixture.componentRef.setInput('lines', lines);
    fixture.componentRef.setInput('from', 0);
    fixture.componentRef.setInput('to', 600);
    fixture.componentRef.setInput('step', 60);
    fixture.componentRef.setInput('unit', 'percent');
    fixture.componentRef.setInput('max', 100);
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  it('draws one path per line and leaves a gap where values are missing', () => {
    const element = render([
      {
        label: 'CPU',
        points: [
          { time: 0, value: 0 },
          { time: 60, value: 50 },
          { time: 300, value: 100 },
        ],
      },
    ]);

    const paths = element.querySelectorAll('path');
    expect(paths.length).toBe(1);
    // The first two points are joined; the third starts a new segment after the gap.
    expect(paths[0].getAttribute('d')).toBe('M50.0 100.0h0L150.0 50.0M550.0 0.0h0');
  });

  it('shows a legend only when there are several lines', () => {
    const single = render([{ label: 'CPU', points: [] }]);
    expect(single.querySelector('.legend')).toBeNull();

    const several = render([
      { label: 'CPU', points: [] },
      { label: 'Memory', points: [] },
    ]);
    expect(several.querySelector('.legend')?.textContent).toContain('Memory');
  });

  it('shows the values at the pointer', () => {
    const element = render([
      { label: 'CPU', points: [{ time: 120, value: 12.5 }] },
      { label: 'Memory', points: [] },
    ]);
    const plot = element.querySelector('.plot') as HTMLElement;
    plot.getBoundingClientRect = () => ({ left: 0, width: 600 }) as DOMRect;

    plot.dispatchEvent(new MouseEvent('pointermove', { clientX: 150 }));
    fixture.detectChanges();

    const tooltip = element.querySelector('.tooltip')?.textContent ?? '';
    expect(tooltip).toContain('12.5 %');
    expect(tooltip).toContain('–');

    plot.dispatchEvent(new MouseEvent('pointerleave'));
    fixture.detectChanges();
    expect(element.querySelector('.tooltip')).toBeNull();
  });
});

describe('niceCeiling', () => {
  it('rounds temperatures up to the next ten degrees', () => {
    expect(niceCeiling(61, 'celsius')).toBe(70);
    expect(niceCeiling(0, 'celsius')).toBe(10);
  });

  it('rounds other values up to 1, 2 or 5 times a power of ten', () => {
    expect(niceCeiling(3.2, 'watts')).toBe(5);
    expect(niceCeiling(140, 'number')).toBe(200);
    expect(niceCeiling(0, 'perSecond')).toBe(1);
  });

  it('rounds network speeds up to a power of two', () => {
    expect(niceCeiling(300 * 1024, 'bytesPerSecond')).toBe(512 * 1024);
    expect(niceCeiling(0, 'bytesPerSecond')).toBe(1024);
  });
});
