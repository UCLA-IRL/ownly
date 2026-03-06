import { EventEmitter } from 'events';

import type TypedEmitter from 'typed-emitter';
import type { IChatChannel, IProject, IProjectFile, IAgentChannel } from './types';


/**
 * Global event bus for the application.
 */
export const GlobalBus = new EventEmitter() as TypedEmitter<{
  /**
   * Event when the list of chat channels is updated.
   * @param channels List of chat channels
   */
  'chat-channels': (channels: IChatChannel[]) => void;

  /**
   * Event when the list of projects is updated.
   * @param projects List of projects
   */
  'project-list': (projects: IProject[]) => void;

  /**
   * Event when the list of files in the active project is updated.
   * @param uuid UUID of the project that is active
   * @param files List of files in the project
   */
  'project-files': (uuid: string, files: IProjectFile[]) => void;

  /**
   * Various informational errors may be received here.
   * Getting this event means that the error was ignored, and this is informational.
   *
   * 1. Workspace setup failed.
   * 2. FS Sync failures.
   */
  'wksp-error': (error: Error) => void;

  /**
   * Event when connectivity changes.
   * The state is stored in _ndnd_conn_state
   */
  'conn-change': () => void;

  /**
   * Event when agent channels are updated.
   * @param channels List of agent channels
   */
  'agent-channels': (channels: IAgentChannel[]) => void;

  /**
   * Event when owner receives a participant boot-join app payload.
   */
  'boot-join-payload': (
    workspace: string,
    preCertFullName: string,
    preCertKeyName: string,
    payload: Uint8Array,
  ) => void;
}>;
