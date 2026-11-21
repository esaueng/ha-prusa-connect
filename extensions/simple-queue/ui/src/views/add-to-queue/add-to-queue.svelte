<!--
  The section of the print dialog: put the file on the queues of the printers
  ticked above instead of printing it now. Each printer answers on its own,
  and the section says where the file landed on each.
-->
<script lang="ts">
  import { Button } from '@prusa/connect-ui/primitives';
  import ListPlusIcon from '@lucide/svelte/icons/list-plus';

  import type { Added } from '../../adding';
  import { localized } from '../../faults';
  import type { Translate } from '../../api';

  let {
    printers,
    t,
    onAdd,
  }: {
    /** The printers ticked in the dialog, by id and name. */
    printers: readonly { id: string; name: string }[];
    t: Translate;
    onAdd: (device: string) => Promise<Added>;
  } = $props();

  let adding = $state(false);
  let results = $state.raw<
    readonly { id: string; name: string; added: Added }[]
  >([]);

  const add = async (): Promise<void> => {
    adding = true;
    const chosen = printers;
    const answers = await Promise.all(
      chosen.map((printer) => onAdd(printer.id)),
    );
    results = chosen.map((printer, index) => ({
      ...printer,
      added: answers[index] ?? {
        kind: 'failed',
        fault: { code: undefined, title: '', detail: undefined },
      },
    }));
    adding = false;
  };

  const lineOf = (name: string, added: Added): string => {
    switch (added.kind) {
      case 'added':
        return t('simple-queue.section.added', {
          printer: name,
          position: added.position,
        });
      case 'started':
        return t('simple-queue.section.started', { printer: name });
      case 'held':
        return t('simple-queue.section.held', {
          printer: name,
          reason: t(`simple-queue.note.${added.note}`),
        });
      case 'failed':
        return t('simple-queue.section.failed', {
          printer: name,
          reason: localized(added.fault, t).title,
        });
    }
  };
</script>

<section
  class="grid min-w-0 grid-cols-1 gap-2 rounded-md border border-border p-3"
  aria-labelledby="simple-queue-section"
  data-part="add-to-queue"
>
  <h3 id="simple-queue-section" class="text-sm font-medium">
    {t('simple-queue.section.title')}
  </h3>
  <p class="text-sm text-muted-foreground">
    {printers.length === 0
      ? t('simple-queue.section.pick')
      : t('simple-queue.section.intro')}
  </p>
  <Button
    variant="secondary"
    class="w-fit"
    disabled={adding || printers.length === 0}
    onclick={() => void add()}
    data-part="add"
  >
    <ListPlusIcon aria-hidden="true" class="size-4" />
    {adding
      ? t('simple-queue.section.adding')
      : t('simple-queue.section.add', {
          count: Math.max(printers.length, 1),
        })}
  </Button>
  {#if results.length > 0}
    <ul class="grid grid-cols-1 gap-1 text-sm" aria-live="polite">
      {#each results as result (result.id)}
        <li
          class={result.added.kind === 'failed'
            ? 'text-destructive'
            : 'text-foreground'}
          data-part="added"
          data-kind={result.added.kind}
        >
          {lineOf(result.name, result.added)}
        </li>
      {/each}
    </ul>
  {/if}
</section>
