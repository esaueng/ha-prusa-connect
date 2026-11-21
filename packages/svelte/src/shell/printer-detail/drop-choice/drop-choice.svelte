<!--
  The question a file dropped on a printer asks before anything goes up:
  print it now, or only copy it to the printer. Confirm closes it, and the
  files go on their way where every other send is followed. Each action a
  module adds in the device-upload-action slot, such as Add to queue, is a
  choice too: the file is copied, and the action runs while it is on its way.

  A printer prints one file at a time, so of several files the person picks
  the one to print, and the rest are only copied. A printer that cannot start
  a print now says why, in the words the print dialog uses for it. Cancel and
  closing the dialog send nothing.

  Files a module sent open it with that module's choice picked, and ask here
  whether to keep a copy in a storage, which the page asks beside its drop
  zone for a file dropped there.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { formatList, t } from '../../../i18n';
  import AlertDialog from '../../../primitives/alert-dialog/alert-dialog.svelte';
  import AlertDialogAction from '../../../primitives/alert-dialog/alert-dialog-action.svelte';
  import AlertDialogCancel from '../../../primitives/alert-dialog/alert-dialog-cancel.svelte';
  import AlertDialogContent from '../../../primitives/alert-dialog/alert-dialog-content.svelte';
  import AlertDialogDescription from '../../../primitives/alert-dialog/alert-dialog-description.svelte';
  import AlertDialogFooter from '../../../primitives/alert-dialog/alert-dialog-footer.svelte';
  import AlertDialogHeader from '../../../primitives/alert-dialog/alert-dialog-header.svelte';
  import AlertDialogTitle from '../../../primitives/alert-dialog/alert-dialog-title.svelte';
  import { Label } from '../../../primitives/label';
  import { RadioGroup, RadioGroupItem } from '../../../primitives/radio-group';
  import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
  } from '../../../primitives/select';
  import { SaveToStorage } from '../save-to-storage';
  import type { DropChoiceProps } from './types';

  let {
    names,
    printer,
    printTarget,
    actions = [],
    preset,
    keep,
    onConfirm,
    onCancel,
  }: DropChoiceProps = $props();

  const keyOf = (action: { namespace: string; id: string }): string =>
    `action:${action.namespace}.${action.id}`;

  const count = $derived(names?.length ?? 0);
  const several = $derived(count > 1);
  const printable = $derived(count > 0 && printTarget?.ready === true);

  // Why Print now is not offered, said under it in place of what it does.
  const blocked = $derived.by((): string | undefined => {
    if (printable) return undefined;
    if (printTarget?.reasonKey === undefined) return undefined;

    return t(printTarget.reasonKey, {
      states: formatList(printTarget.reasonStates ?? []),
    });
  });

  // The pick, set when the dialog opens: Print now on a printer that can
  // print, copy otherwise. It is set once, so a printer that changes state
  // while the dialog is open does not move the pick under the person.
  let picked = $state<string | undefined>(undefined);
  $effect.pre(() => {
    if (names === undefined || picked !== undefined) return;
    picked = untrack(() => {
      if (preset !== undefined) return keyOf(preset);

      return printable ? 'print' : 'copy';
    });
  });
  const choice = $derived(picked ?? 'copy');

  // Which of several files to print, by its place among the files dropped,
  // so two files of the same name stay two. The first one until the person
  // picks another.
  let chosen = $state(0);
  const printing = $derived(
    names !== undefined && chosen < names.length ? chosen : 0,
  );

  // Print now picked on a printer that has since stopped being able to print
  // is not quietly turned into a copy: Confirm waits for another pick.
  const stale = $derived(choice === 'print' && !printable);

  const confirm = (): void => {
    if (stale) return;
    const print = choice === 'print' ? printing : undefined;
    const action = actions.find((one) => keyOf(one) === choice);
    picked = undefined;
    chosen = 0;
    onConfirm(print, action);
  };
</script>

<AlertDialog
  open={names !== undefined}
  onOpenChange={(open: boolean) => {
    if (open) return;
    picked = undefined;
    chosen = 0;
    onCancel();
  }}
>
  <AlertDialogContent>
    <AlertDialogHeader>
      <AlertDialogTitle
        >{t('printerDetailFiles.drop.title', {
          count,
          printer,
        })}</AlertDialogTitle
      >
      <AlertDialogDescription>
        {#if count === 1}
          {names?.[0]}
        {:else}
          <!-- One name a line; the title says how many. -->
          <ul class="flex flex-col gap-0.5">
            {#each names ?? [] as name, at (at)}
              <li>{name}</li>
            {/each}
          </ul>
        {/if}
      </AlertDialogDescription>
    </AlertDialogHeader>
    <RadioGroup
      bind:value={
        () => choice,
        (next: string) => {
          picked = next;
        }
      }
      aria-label={t('printerDetailFiles.drop.title', { count, printer })}
      class="gap-3"
    >
      <Label variant="option" data-disabled={printable ? undefined : ''}>
        <RadioGroupItem value="print" disabled={!printable} class="mt-0.5" />
        <span class="flex min-w-0 flex-1 flex-col gap-0.5">
          <span>
            {t(
              several
                ? 'printerDetailFiles.drop.printOne'
                : 'printerDetailFiles.drop.print',
            )}
          </span>
          <span class="text-sm text-muted-foreground">
            {blocked ??
              t(
                several
                  ? 'printerDetailFiles.drop.printOneNote'
                  : 'printerDetailFiles.drop.printNote',
              )}
          </span>
        </span>
      </Label>
      {#if several && printable}
        <!-- Indented by the radio and the gap beside it, so the list lines up
             with the option's text. -->
        <div class="min-w-0 pl-6">
          <!-- Outside the option's label, so opening the list does not pick
               the option a second time. It stays while Only copy is picked,
               greyed, so the dialog keeps its height between the two. -->
          <Select
            disabled={choice !== 'print'}
            value={String(printing)}
            items={(names ?? []).map((name, at) => ({
              value: String(at),
              label: name,
            }))}
            onValueChange={(next: string) => {
              chosen = Number(next);
            }}
          >
            <SelectTrigger
              class="max-w-full min-w-0 font-mono"
              aria-label={t('printerDetailFiles.drop.printWhich')}
            >
              <!-- Its own span and not SelectValue, which the trigger lays out as
                   a flex row that clips a long name without an ellipsis. -->
              <span class="min-w-0 truncate">{names?.[printing]}</span>
            </SelectTrigger>
            <!-- As wide as the trigger, so a long name wraps on a phone instead
                 of running past the screen. -->
            <SelectContent class="max-w-(--radix-select-trigger-width)">
              {#each names ?? [] as name, at (at)}
                <SelectItem
                  value={String(at)}
                  label={name}
                  class="wrap-anywhere"
                >
                  {name}
                </SelectItem>
              {/each}
            </SelectContent>
          </Select>
        </div>
      {/if}
      <Label variant="option">
        <RadioGroupItem value="copy" class="mt-0.5" />
        <span class="flex flex-col gap-0.5">
          <span>{t('printerDetailFiles.drop.copy')}</span>
          <span class="text-sm text-muted-foreground">
            {t('printerDetailFiles.drop.copyNote', { count })}
          </span>
        </span>
      </Label>
      {#each actions as action (keyOf(action))}
        <Label variant="option">
          <RadioGroupItem value={keyOf(action)} class="mt-0.5" />
          <span
            class="flex flex-col gap-0.5"
            data-slot="device-upload-action"
            data-module={action.namespace}
            data-section={action.id}
          >
            <span>{action.title}</span>
            <span class="text-sm text-muted-foreground">
              {t('printerDetailFiles.drop.actionNote')}
            </span>
          </span>
        </Label>
      {/each}
    </RadioGroup>
    {#if keep}
      <SaveToStorage {...keep} />
    {/if}
    <AlertDialogFooter>
      <AlertDialogCancel size="lg">{t('primitives.cancel')}</AlertDialogCancel>
      <AlertDialogAction size="lg" disabled={stale} onclick={confirm}>
        {t('printerDetailFiles.drop.confirm')}
      </AlertDialogAction>
    </AlertDialogFooter>
  </AlertDialogContent>
</AlertDialog>
