import { describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import ReplaceQuestion from './replace-question.svelte';

describe('ReplaceQuestion', () => {
  it('asks a copy whether to keep both files, replace the one there, or neither', async () => {
    const onReplace = vi.fn();
    const onKeepBoth = vi.fn();
    const onKeep = vi.fn();
    await render(ReplaceQuestion, {
      props: { name: 'a.bgcode', onReplace, onKeepBoth, onKeep },
    });

    await expect
      .element(page.getByRole('button', { name: /Print the printer.s file/u }))
      .not.toBeInTheDocument();
    await page.getByRole('button', { name: 'Keep both' }).click();
    await page.getByRole('button', { name: 'Replace' }).click();
    await page.getByRole('button', { name: 'Cancel' }).click();

    expect(onKeepBoth).toHaveBeenCalledOnce();
    expect(onReplace).toHaveBeenCalledOnce();
    expect(onKeep).toHaveBeenCalledOnce();
  });

  it('offers a module choice the file the printer holds', async () => {
    const onUseHeld = vi.fn();
    await render(ReplaceQuestion, {
      props: {
        name: 'a.bgcode',
        onReplace: vi.fn(),
        onKeepBoth: vi.fn(),
        onUseHeld,
        onKeep: vi.fn(),
      },
    });

    await page.getByRole('button', { name: /Use the printer.s file/u }).click();

    expect(onUseHeld).toHaveBeenCalledOnce();
  });

  it('offers a print the file on the printer, both files, a replace, or neither', async () => {
    const onReplace = vi.fn();
    const onKeepBoth = vi.fn();
    const onPrintHeld = vi.fn();
    const onKeep = vi.fn();
    await render(ReplaceQuestion, {
      props: {
        name: 'a.bgcode',
        print: true,
        onReplace,
        onKeepBoth,
        onPrintHeld,
        onKeep,
      },
    });

    await page
      .getByRole('button', { name: /Print the printer.s file/u })
      .click();
    await page.getByRole('button', { name: 'Keep both and print' }).click();
    await page.getByRole('button', { name: 'Replace and print' }).click();
    await page.getByRole('button', { name: 'Cancel print' }).click();

    expect(onPrintHeld).toHaveBeenCalledOnce();
    expect(onKeepBoth).toHaveBeenCalledOnce();
    expect(onReplace).toHaveBeenCalledOnce();
    expect(onKeep).toHaveBeenCalledOnce();
  });

  it('cannot replace a file the printer holds read-only, says why, and still keeps both', async () => {
    const onKeep = vi.fn();
    const onKeepBoth = vi.fn();
    await render(ReplaceQuestion, {
      props: {
        name: 'a.bgcode',
        readOnly: true,
        onReplace: vi.fn(),
        onKeepBoth,
        onKeep,
      },
    });

    await expect
      .element(
        page.getByText(
          /a\.bgcode is already on this printer and cannot be replaced/u,
        ),
      )
      .toBeInTheDocument();
    await expect
      .element(page.getByText(/does not let this file be changed now/u))
      .toBeInTheDocument();
    await expect
      .element(page.getByRole('button', { name: 'Replace' }))
      .not.toBeInTheDocument();
    // Keep both deletes nothing, so the printer lets it go.
    await page.getByRole('button', { name: 'Keep both' }).click();
    await page.getByRole('button', { name: 'Cancel' }).click();

    expect(onKeepBoth).toHaveBeenCalledOnce();
    expect(onKeep).toHaveBeenCalledOnce();
  });

  it('still prints the read-only file the printer holds', async () => {
    const onPrintHeld = vi.fn();
    await render(ReplaceQuestion, {
      props: {
        name: 'a.bgcode',
        print: true,
        readOnly: true,
        onReplace: vi.fn(),
        onKeepBoth: vi.fn(),
        onPrintHeld,
        onKeep: vi.fn(),
      },
    });

    await expect
      .element(page.getByRole('button', { name: 'Replace and print' }))
      .not.toBeInTheDocument();
    await page
      .getByRole('button', { name: /Print the printer.s file/u })
      .click();

    expect(onPrintHeld).toHaveBeenCalledOnce();
  });
});
