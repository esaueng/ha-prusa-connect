<!-- A screen for the app's error boundary: it counts its mounts and fails to
     render while `screen.failing` is set. It keeps the slot registry the app
     hands it. -->
<script lang="ts">
  import type { AppContext } from '../src/lib/screens';
  import { screen } from './screen-probe';

  let { ctx }: { ctx?: AppContext } = $props();

  screen.mounts += 1;
  $effect.pre(() => {
    screen.registry = ctx?.slots.registry;
  });
  if (screen.failing) throw new Error('this screen cannot be drawn');
</script>

<div data-testid="counting-screen"></div>
