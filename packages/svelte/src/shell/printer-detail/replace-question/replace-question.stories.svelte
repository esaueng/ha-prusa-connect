<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import ReplaceQuestion from './replace-question.svelte';

  const { Story } = defineMeta({
    title: 'Parts/Screen components/Printer Detail/Replace Question',
    component: ReplaceQuestion,
  });
</script>

<!-- A file sent to the printer meets one it already holds under that name. -->
<Story
  name="The Printer Already Holds The Name"
  args={{
    name: 'Bracket_Left_Reinforced_v4_FINAL.gcode',
    onReplace: () => undefined,
    onKeepBoth: () => undefined,
    onKeep: () => undefined,
  }}
/>

<!-- The same, for a file dropped with Print now: replace it and print, or print the printer's file. -->
<Story
  name="Replace And Print"
  args={{
    name: 'Bracket_Left_Reinforced_v4_FINAL.gcode',
    print: true,
    onReplace: () => undefined,
    onKeepBoth: () => undefined,
    onPrintHeld: () => undefined,
    onKeep: () => undefined,
  }}
/>

<!-- The printer lists its file read-only, as a Buddy printer does for a file open on its screen, so it cannot be replaced now. -->
<Story
  name="The Printer's File Is Read-Only"
  args={{
    name: 'Bracket_Left_Reinforced_v4_FINAL.gcode',
    readOnly: true,
    onReplace: () => undefined,
    onKeepBoth: () => undefined,
    onKeep: () => undefined,
  }}
/>

<!-- The same for a file dropped with Print now: the printer's file can still be printed. -->
<Story
  name="The Printer's File Is Read-Only, For A Print"
  args={{
    name: 'Bracket_Left_Reinforced_v4_FINAL.gcode',
    print: true,
    readOnly: true,
    onReplace: () => undefined,
    onKeepBoth: () => undefined,
    onPrintHeld: () => undefined,
    onKeep: () => undefined,
  }}
/>
