<!--
  The box that leads a queued file: the picture of its sliced plate when the
  file carries one, and a file glyph when it does not or cannot be read. It is
  the box the Files tab and the Jobs table lead a file with, so a file looks
  the same wherever it is listed. The picture is asked for only when the row
  comes into view.
-->
<script lang="ts">
  import type { ClassValue } from 'svelte/elements';
  import { FileThumbnail } from '@prusa/connect-ui/primitives';
  import FileCode2Icon from '@lucide/svelte/icons/file-code-2';

  import { type Pictured, thumbnailOf } from '../../thumbnails';

  let {
    device,
    entry,
    name,
    class: className,
  }: {
    /** The printer the queue is of. Without it only the glyph is drawn. */
    device: string | undefined;
    /**
     * A file still on its way to the printer is not asked about: the printer
     * has no picture to read yet, so the glyph stands in until it has landed.
     */
    entry: Pictured & { arriving?: boolean | undefined };
    /** The file's name, which names the picture for a screen reader. */
    name: string;
    /** A bigger box, for a card led by the picture. */
    class?: ClassValue | undefined;
  } = $props();

  let open = $state(false);
  const url = $derived(
    device === undefined || entry.arriving === true
      ? undefined
      : thumbnailOf(device, entry),
  );
</script>

<FileThumbnail
  {name}
  thumbnailUrl={url}
  glyph={FileCode2Icon}
  class={className}
  bind:open
/>
