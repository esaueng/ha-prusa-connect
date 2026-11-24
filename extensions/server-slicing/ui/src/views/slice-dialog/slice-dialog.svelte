<!--
  The question a person answers before a project is sliced: which bed, what
  kind of G-code, and whether a file of the same name may be replaced. A
  file's page opens it through the Slice button, with the project named.

  The G-code goes into the folder of the project, under a name the module
  makes. Sending it answers the new job's id, which the page then opens.
-->
<script lang="ts">
  import {
    Button,
    Checkbox,
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    FaultAlert,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
  } from '@prusa/connect-ui/primitives';
  import type { FaultText } from '@prusa/connect-ui/i18n';
  import { parseFileRef } from '@prusa/connect-ui/slots';

  import type { SubmitBody, Translate } from '../../api';

  let {
    project,
    profiles = [],
    t,
    submit,
    onClose,
    onSliced,
    fault,
  }: {
    /** The project, as `<storage>:<path>`. */
    project: string;
    profiles?: string[] | undefined;
    t: Translate;
    submit: (body: SubmitBody) => Promise<string | undefined>;
    onClose: () => void;
    /** Told the id of the job the server queued. */
    onSliced: (id: string) => void;
    /** Why the server refused the last action of the page. */
    fault?: FaultText | undefined;
  } = $props();

  const uid = $props.id();
  // A project holds at most nine beds.
  const BEDS = Array.from({ length: 9 }, (_, at) => String(at + 1));

  const file = $derived(parseFileRef(project));
  const name = $derived(file?.path.split('/').at(-1) ?? project);
  const stl = $derived(name.toLowerCase().endsWith('.stl'));

  let bed = $state('1');
  let profile = $state('');
  const needsProfile = $derived(stl && !profiles.includes(profile));
  let format = $state<'' | 'bgcode' | 'gcode'>('');
  let overwrite = $state(false);
  let sending = $state(false);
  // A refusal belongs here only once this dialog has sent something.
  let sent = $state(false);

  const FORMATS = [
    { value: '', key: 'slicer.slice.formatProject' },
    { value: 'bgcode', key: 'slicer.slice.formatBinary' },
    { value: 'gcode', key: 'slicer.slice.formatText' },
  ] as const;

  const send = async (): Promise<void> => {
    if (file === undefined || needsProfile) return;
    sending = true;
    sent = true;
    try {
      const id = await submit({
        source: { storage: file.storage, path: file.path },
        ...(stl
          ? { profile }
          : { selection: { setup: 0, bed: Number(bed) - 1 } }),
        format,
        overwrite,
      });
      if (id !== undefined) onSliced(id);
    } finally {
      sending = false;
    }
  };
</script>

<Dialog
  open
  onOpenChange={(open: boolean) => {
    if (!open) onClose();
  }}
>
  <DialogContent data-part="slice-dialog">
    <DialogHeader>
      <DialogTitle
        >{t(stl ? 'slicer.slice.titleSTL' : 'slicer.slice.title')}</DialogTitle
      >
      <DialogDescription class="break-words">
        {t(stl ? 'slicer.slice.descriptionSTL' : 'slicer.slice.description', {
          name,
        })}
      </DialogDescription>
    </DialogHeader>

    {#if sent}
      <FaultAlert {fault} />
    {/if}

    {#if file === undefined}
      <FaultAlert
        fault={{
          code: undefined,
          title: t('slicer.slice.noFile'),
          detail: undefined,
        }}
      />
    {:else}
      <div class="grid grid-cols-1 gap-4">
        {#if stl}
          {#if profiles.length === 0}
            <p class="text-sm text-muted-foreground">
              {t('slicer.slice.noProfiles')}
            </p>
          {:else}
            <div class="grid grid-cols-1 gap-2">
              <Label for="{uid}-profile">{t('slicer.slice.profile')}</Label>
              <Select bind:value={profile}>
                <SelectTrigger id="{uid}-profile">
                  <SelectValue
                    >{profile || t('slicer.slice.chooseProfile')}</SelectValue
                  >
                </SelectTrigger>
                <SelectContent>
                  {#each profiles as name (name)}
                    <SelectItem value={name} label={name}>{name}</SelectItem>
                  {/each}
                </SelectContent>
              </Select>
            </div>
          {/if}
        {:else}
          <div class="grid grid-cols-1 gap-2">
            <Label for="{uid}-bed">{t('slicer.slice.bed')}</Label>
            <Select bind:value={bed}>
              <SelectTrigger id="{uid}-bed">
                <SelectValue
                  >{t('slicer.slice.bedNumber', { n: bed })}</SelectValue
                >
              </SelectTrigger>
              <SelectContent>
                {#each BEDS as number (number)}
                  <SelectItem
                    value={number}
                    label={t('slicer.slice.bedNumber', { n: number })}
                  >
                    {t('slicer.slice.bedNumber', { n: number })}
                  </SelectItem>
                {/each}
              </SelectContent>
            </Select>
            <p class="text-xs text-muted-foreground">
              {t('slicer.slice.bedHint')}
            </p>
          </div>
        {/if}

        <div class="grid grid-cols-1 gap-2">
          <Label for="{uid}-format">{t('slicer.slice.format')}</Label>
          <Select bind:value={format}>
            <SelectTrigger id="{uid}-format">
              <SelectValue>
                {t(
                  format === '' && stl
                    ? 'slicer.slice.formatProfile'
                    : (FORMATS.find((one) => one.value === format)?.key ??
                        'slicer.slice.formatProject'),
                )}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {#each FORMATS as one (one.value)}
                {@const label = t(
                  stl && one.value === ''
                    ? 'slicer.slice.formatProfile'
                    : one.key,
                )}
                <SelectItem value={one.value} {label}>
                  {label}
                </SelectItem>
              {/each}
            </SelectContent>
          </Select>
        </div>

        <div class="flex items-center gap-2">
          <Checkbox
            id="{uid}-overwrite"
            checked={overwrite}
            onCheckedChange={(checked: boolean) => {
              overwrite = checked;
            }}
          />
          <Label for="{uid}-overwrite" variant="option">
            {t('slicer.slice.overwrite')}
          </Label>
        </div>
      </div>
    {/if}

    <DialogFooter>
      <Button variant="secondary" onclick={onClose}>
        {t('slicer.slice.close')}
      </Button>
      <Button
        disabled={file === undefined || needsProfile || sending}
        onclick={() => void send()}
      >
        {t('slicer.slice.send')}
      </Button>
    </DialogFooter>
  </DialogContent>
</Dialog>
