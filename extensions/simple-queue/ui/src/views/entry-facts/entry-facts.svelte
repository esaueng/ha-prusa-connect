<!--
  What is known about a queued file, under its name, each fact a label over
  its value: where the file is, what the slicer wrote about it (the printer
  it was sliced for, the layer height, the material), its size, and who added
  it when the row says so. A fact the file does not name is left out.
-->
<script lang="ts">
  import { formatBytes, formatNumber } from '@prusa/connect-ui/i18n';

  import type { Entry, Translate } from '../../api';
  import { folderOf } from '../../queue-text';

  let {
    entry,
    addedBy,
    t,
  }: {
    entry: Entry;
    /** Who added the file and when, for a row that names it. */
    addedBy?: string | undefined;
    t: Translate;
  } = $props();

  const facts = $derived(
    [
      {
        key: 'location',
        value:
          entry.storage === ''
            ? folderOf(entry)
            : t('simple-queue.facts.inStorage', { folder: folderOf(entry) }),
      },
      { key: 'printer', value: entry.printer_model },
      {
        key: 'layer',
        value:
          entry.layer_height_mm &&
          formatNumber(entry.layer_height_mm, {
            style: 'unit',
            unit: 'millimeter',
            maximumFractionDigits: 2,
          }),
      },
      // One material for each tool, joined by a semicolon in the file. A
      // material several tools share is named once.
      {
        key: 'material',
        value: [...new Set(entry.material?.split(';'))].join(', '),
      },
      { key: 'size', value: entry.size && formatBytes(entry.size) },
      { key: 'added', value: addedBy },
    ].filter((fact) => Boolean(fact.value)),
  );
</script>

{#if facts.length > 0}
  <dl
    class="flex min-w-0 flex-wrap gap-x-6 gap-y-2 text-xs"
    data-part="entry-facts"
  >
    {#each facts as fact (fact.key)}
      <div class="grid min-w-0 gap-0.5" data-fact={fact.key}>
        <dt class="text-muted-foreground">
          {t(`simple-queue.facts.${fact.key}`)}
        </dt>
        <dd class="truncate text-sm tabular-nums">{fact.value}</dd>
      </div>
    {/each}
  </dl>
{/if}
