<!--
  The question a file sent to this printer asks when the printer already holds
  a file under its name. The printer's file is deleted only on Replace, so
  every other answer and closing the dialog keep it.

  The buttons say each answer, so there is no sentence. Keep both sends this
  file under a free name the server picks, such as part[1].bgcode. Replace
  sends it in place of the printer's. Cancel sends nothing. A file sent to be
  printed has one more: Print the printer's file starts the one already there
  and sends nothing. A file dropped with a module's choice, such as Add to
  queue, has Use the printer's file, which hands that file to the module.

  A file the printer lists read-only cannot be replaced: the printer refuses
  to delete it, most likely because it is open on its screen or still
  arriving. The question says so and offers no Replace. Keep both still
  goes, as it deletes nothing.
-->
<script lang="ts">
  import { t } from '../../../i18n';
  import AlertDialog from '../../../primitives/alert-dialog/alert-dialog.svelte';
  import AlertDialogAction from '../../../primitives/alert-dialog/alert-dialog-action.svelte';
  import AlertDialogCancel from '../../../primitives/alert-dialog/alert-dialog-cancel.svelte';
  import AlertDialogContent from '../../../primitives/alert-dialog/alert-dialog-content.svelte';
  import AlertDialogDescription from '../../../primitives/alert-dialog/alert-dialog-description.svelte';
  import AlertDialogHeader from '../../../primitives/alert-dialog/alert-dialog-header.svelte';
  import AlertDialogTitle from '../../../primitives/alert-dialog/alert-dialog-title.svelte';
  import type { ReplaceQuestionProps } from './types';

  let {
    name,
    print,
    readOnly = false,
    onReplace,
    onKeepBoth,
    onPrintHeld,
    onUseHeld,
    onKeep,
  }: ReplaceQuestionProps = $props();

  const printing = $derived(print === true && onPrintHeld !== undefined);
</script>

<AlertDialog
  open={name !== undefined}
  onOpenChange={(open: boolean) => {
    if (!open) onKeep();
  }}
>
  <AlertDialogContent>
    <AlertDialogHeader>
      <AlertDialogTitle>
        {t(
          readOnly
            ? 'printerDetailFiles.replace.readOnlyTitle'
            : 'printerDetailFiles.replace.title',
          { name: name ?? '' },
        )}
      </AlertDialogTitle>
      {#if readOnly}
        <AlertDialogDescription>
          {t('printerDetailFiles.replace.readOnly')}
        </AlertDialogDescription>
      {/if}
    </AlertDialogHeader>
    <!-- The answers do not fit the footer's row, so they stay stacked at
         every width, the ones that lose nothing on top and Cancel last. The
         footer's own classes set a row on a wide window, so this is a column
         of its own. -->
    <div
      data-slot="replace-question-answers"
      class="flex flex-col-reverse gap-2"
    >
      <AlertDialogCancel variant="ghost" size="lg">
        {printing
          ? t('printerDetailFiles.replace.cancelPrint')
          : t('primitives.cancel')}
      </AlertDialogCancel>
      {#if !readOnly}
        <AlertDialogAction variant="secondary" size="lg" onclick={onReplace}>
          {printing
            ? t('printerDetailFiles.replace.replacePrint')
            : t('printerDetailFiles.replace.confirm')}
        </AlertDialogAction>
      {/if}
      <AlertDialogAction variant="secondary" size="lg" onclick={onKeepBoth}>
        {printing
          ? t('printerDetailFiles.replace.keepBothPrint')
          : t('printerDetailFiles.replace.keepBoth')}
      </AlertDialogAction>
      {#if printing}
        <AlertDialogAction variant="secondary" size="lg" onclick={onPrintHeld}>
          {t('printerDetailFiles.replace.printHeld')}
        </AlertDialogAction>
      {:else if onUseHeld !== undefined}
        <AlertDialogAction variant="secondary" size="lg" onclick={onUseHeld}>
          {t('printerDetailFiles.replace.useHeld')}
        </AlertDialogAction>
      {/if}
    </div>
  </AlertDialogContent>
</AlertDialog>
