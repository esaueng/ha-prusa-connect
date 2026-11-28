// The service layer: this module's routes, typed from its own OpenAPI
// fragment, bound onto the client the host handed over. Every request goes
// through it, so the core authorizes each one as the person on the page.
import type { ModuleHost } from '@prusa/connect-ui/slots';
import { bind, type components, type Operations } from 'server-slicing-client';

export type SlicerApi = Pick<
  Operations,
  | 'listSlicers'
  | 'listJobs'
  | 'installSlicer'
  | 'refreshSlicers'
  | 'removeSlicer'
  | 'pinSlicer'
  | 'cancelJob'
  | 'submitJob'
  | 'readJob'
>;
export type Slicers = components['schemas']['Slicers'];
export type Offer = components['schemas']['Offer'];
export type InstalledSlicer = components['schemas']['Slicer'];
export type InstallProgress = components['schemas']['InstallProgress'];
export type Catalogue = components['schemas']['Catalogue'];
export type Job = components['schemas']['Job'];
export type JobList = components['schemas']['JobList'];
export type STLBed = components['schemas']['STLBed'];
export type SubmitBody = components['schemas']['SubmitBody'];
export type ProfileSettings = components['schemas']['ProfileSettings'];

/** The module's routes, on the host's client. */
export const apiOn = (host: Pick<ModuleHost, 'client'>): SlicerApi =>
  bind(host.client);

/** Translates one of this module's texts. */
export type Translate = ModuleHost['t'];
