import { describe, expect, it, vi } from 'vitest';
import { page, userEvent } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { expectNoViolations } from '../../test/axe';
import DetailTabs from './detail-tabs.svelte';
import DetailTabsProbe from '../../test/detail-tabs-probe.svelte';

describe('DetailTabs', () => {
  it('draws one trigger per tab and marks the open one', async () => {
    await render(DetailTabsProbe, { props: { active: 'files' } });

    const labels = [
      ...document.querySelectorAll('[data-slot="tabs-trigger"]'),
    ].map((trigger) => trigger.textContent.trim());
    expect(labels).toEqual(['Status', 'Files', 'Info']);

    const open = document.querySelector('[data-state="active"]');
    expect(open?.textContent.trim()).toBe('Files');
  });

  // Which tab is open belongs to the screen, so the part reports the press and
  // changes nothing of its own.
  it('hands back the tab that was pressed', async () => {
    const onChange = vi.fn();
    await render(DetailTabsProbe, { props: { active: 'status', onChange } });

    await page.getByRole('tab', { name: 'Info' }).click();

    expect(onChange).toHaveBeenCalledWith('info');
  });

  it('draws the panel of the open tab', async () => {
    const { container } = await render(DetailTabsProbe, {
      props: { active: 'status' },
    });

    expect(
      container.querySelector('[data-testid="status-panel"]'),
    ).not.toBeNull();
  });

  // A screen ships its frame before its tabs, and is never broken in between.
  it('draws the skeleton for a tab with no panel', async () => {
    const { container } = await render(DetailTabsProbe, {
      props: { active: 'info' },
    });

    expect(container.querySelector('[data-testid="status-panel"]')).toBeNull();
    expect(
      container.querySelectorAll('[data-slot="skeleton"]').length,
    ).toBeGreaterThan(0);
  });

  it('reaches every tab by arrow key, and the scroller takes no tab stop', async () => {
    await render(DetailTabsProbe, { props: { active: 'status' } });

    // Chrome takes the next tab stop from the last focused position, which
    // outlives the element another test clicked, so the walk starts from an
    // anchor of this test's own.
    const start = document.createElement('button');
    document.body.prepend(start);
    start.focus();

    await userEvent.tab();
    await expect
      .element(page.getByRole('tab', { name: 'Status' }))
      .toHaveFocus();
    await userEvent.keyboard('{ArrowRight}');
    await expect
      .element(page.getByRole('tab', { name: 'Files' }))
      .toHaveFocus();

    start.remove();
  });

  // A tab whose opening loads a new page, such as a cross-origin isolated
  // module tab, does not open on focus. Otherwise a keyboard user could never
  // move past it.
  it('opens a tab on Enter rather than on arrow-key focus while one loads a page', async () => {
    const onChange = vi.fn();
    await render(DetailTabsProbe, {
      props: { active: 'files', onChange, loadsPage: true },
    });

    page.getByRole('tab', { name: 'Files' }).element().focus();
    await userEvent.keyboard('{ArrowRight}');
    await expect.element(page.getByRole('tab', { name: 'Info' })).toHaveFocus();
    expect(onChange).not.toHaveBeenCalled();

    await userEvent.keyboard('{Enter}');
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('info');
  });

  it('opens a tab on arrow-key focus while none loads a page', async () => {
    const onChange = vi.fn();
    await render(DetailTabsProbe, { props: { active: 'files', onChange } });

    page.getByRole('tab', { name: 'Files' }).element().focus();
    await userEvent.keyboard('{ArrowRight}');

    expect(onChange).toHaveBeenCalledWith('info');
  });

  // A strip drawn far down the page, such as one in a module's view, must
  // not move the page when it opens. A narrow strip still brings the open
  // tab into its own sight.
  it('scrolls the strip to the open tab and leaves the page where it is', async () => {
    const spacer = document.createElement('div');
    spacer.style.height = '3000px';
    document.body.prepend(spacer);
    window.scrollTo(0, 0);

    const { container, rerender } = await render(DetailTabsProbe, {
      props: { active: 'status' },
    });
    container.style.width = '120px';
    await rerender({ active: 'info' });
    await expect
      .element(page.getByRole('tab', { name: 'Info' }))
      .toHaveAttribute('aria-selected', 'true');

    expect(window.scrollY).toBe(0);
    const scroller = page
      .getByRole('tab', { name: 'Info' })
      .element()
      .closest<HTMLElement>('.overflow-x-auto');
    expect(scroller?.scrollLeft).toBeGreaterThan(0);

    spacer.remove();
  });

  // Module tabs join the strip after the screen has loaded.
  it('scrolls to a tab added after mount once it is open', async () => {
    const { container, rerender } = await render(DetailTabsProbe, {
      props: { active: 'status' },
    });
    container.style.width = '120px';
    await rerender({ active: 'status', extra: true });
    await rerender({ active: 'late', extra: true });
    const late = page.getByRole('tab', { name: 'Late module' });
    await expect.element(late).toHaveAttribute('aria-selected', 'true');

    const tab = late.element() as HTMLElement;
    const scroller = tab.closest<HTMLElement>('.overflow-x-auto');
    expect(scroller?.scrollLeft).toBeGreaterThan(0);
    expect(tab.offsetLeft + tab.offsetWidth).toBeLessThanOrEqual(
      (scroller?.scrollLeft ?? 0) + (scroller?.clientWidth ?? 0),
    );
  });

  it('has nothing axe objects to', async () => {
    const { container } = await render(DetailTabsProbe, {
      props: { active: 'status' },
    });

    await expectNoViolations(container);
  });
});

describe('DetailTabs count', () => {
  it('draws the count in a badge after the label', async () => {
    await render(DetailTabs, {
      props: {
        active: 'jobs',
        onChange: () => undefined,
        tabs: [{ id: 'jobs', label: 'Jobs', count: 3 }],
      },
    });
    const badge = document.querySelector(
      '[data-slot="tabs-trigger"] [data-slot="badge"]',
    );
    expect(badge?.textContent.trim()).toBe('3');
  });
});
