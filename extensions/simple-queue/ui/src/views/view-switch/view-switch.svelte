<!--
  The choice between the two ways a queue is drawn: on a timeline or as
  cards. The page that holds it keeps the choice for the person.
-->
<script lang="ts">
  import { ViewModeToggle } from '@prusa/connect-ui/primitives';
  import ChartGanttIcon from '@lucide/svelte/icons/chart-gantt';
  import LayoutGridIcon from '@lucide/svelte/icons/layout-grid';

  import type { Translate } from '../../api';
  import { type BoardMode, isBoardMode } from '../../board-mode';

  let {
    mode,
    onMode,
    t,
  }: {
    mode: BoardMode;
    onMode: (mode: BoardMode) => void;
    t: Translate;
  } = $props();

  const modes = $derived([
    {
      value: 'timeline',
      label: t('simple-queue.view.timeline'),
      icon: ChartGanttIcon,
    },
    {
      value: 'cards',
      label: t('simple-queue.view.cards'),
      icon: LayoutGridIcon,
    },
  ]);
</script>

<ViewModeToggle
  value={mode}
  options={modes}
  label={t('simple-queue.view.label')}
  onValueChange={(next: string) => {
    if (isBoardMode(next)) onMode(next);
  }}
/>
