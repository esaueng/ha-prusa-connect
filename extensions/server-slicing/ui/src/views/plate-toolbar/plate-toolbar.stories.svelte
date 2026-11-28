<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';
  import { BoxGeometry } from 'three';

  import { Plate } from '../../plate/plate.svelte';
  import { t } from '../../test/fakes';
  import { bed } from '../prepare-panel/prepare-fixtures';
  import PlateToolbar from './plate-toolbar.svelte';

  const none = (): void => undefined;
  const empty = new Plate(() => bed);
  const full = new Plate(() => bed);
  full.add(
    {
      parts: [
        { geometry: new BoxGeometry(10, 20, 30).toNonIndexed() },
        { geometry: new BoxGeometry(30, 30, 10).toNonIndexed() },
      ],
      preservePlacement: false,
    },
    'bracket.stl',
  );

  const { Story } = defineMeta({
    title: 'Server slicing/PlateToolbar',
    component: PlateToolbar,
    args: {
      plate: empty,
      top: false,
      links: [
        { href: '?tab=slicers', label: 'Slicers' },
        { href: '?tab=jobs', label: 'Jobs' },
        { href: '?tab=api', label: 'API' },
      ],
      t,
      onAdd: none,
      onView: none,
      onArrange: none,
      onCopy: none,
    },
  });
</script>

<Story name="Empty plate" />
<Story name="Parts selected" args={{ plate: full }} />
<Story name="Top view" args={{ plate: full, top: true }} />
<Story name="Reading a model" args={{ adding: true }} />
