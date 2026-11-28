<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import { profileSettings } from '../prepare-panel/prepare-fixtures';
  import PrintSettings from './print-settings.svelte';

  const profiles = {
    ...profileSettings,
    'core-one-pla': { filament_type: 'PLA', layer_height_mm: 0.15 },
  };

  const { Story } = defineMeta({
    title: 'Server slicing/PrintSettings',
    component: PrintSettings,
    args: {
      choices: { settings: {} },
      profiles,
      destinations: [
        { name: 'local', label: 'Local files' },
        { name: 'nas', label: 'Workshop NAS' },
      ],
      printerName: 'Henry',
      t,
      onChange: () => undefined,
    },
  });
</script>

<Story name="Profile settings" />
<Story
  name="Changed settings"
  args={{
    choices: {
      profile: 'core-one-pla',
      storage: 'nas',
      settings: {
        layer_height_mm: 0.28,
        supports: 'build_plate',
        infill_percent: 40,
        walls: 4,
        brim_width_mm: 5,
      },
    },
  }}
/>
<Story
  name="Profile bed differs"
  args={{
    bedNote:
      "The profile's bed is 250 × 220 mm. This printer's bed is another size.",
  }}
/>
<Story name="No saved profile" args={{ profiles: {}, destinations: [] }} />
