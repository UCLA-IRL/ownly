export type IMlsKey = {
  /** MLS key associated with a session ID */
  sessionId: string;
  mlsKey: string;
}

export type IWkspStats = {
  /** Readable label for the space */
  label: string;
  /** Data prefix of the space */
  name: string;
  /** Is the current user the owner */
  owner: boolean;
  /** Workspace ignore certificate lifetime */
  ignore: boolean;
  /** Workspace is pending initial setup */
  pendingSetup?: boolean;
  /** Local access was revoked; require a fresh invitation to rejoin */
  revoked?: boolean;
  /** Last access time */
  lastAccess?: number;
  /** Stable identifier for this local device/workspace profile */
  deviceId?: string;
  /** Whether this local device is the designated master MLS device */
  isMasterDevice?: boolean;

  /** Pre-shared key */
  psk: string;
  /** Dynamic-shared key */
  dsk: string | null;
  /** DSK request key */
  dskExch?: string;
  /** Invitee already published MLS key package request */
  mlsJoinRequested?: boolean;
  mlsJoinRequestedAt?: number;
  mlsJoinAttempts?: number;
  mlsOwnerBootstrapped?: boolean;
  mlsKeys?: IMlsKey[];
  /** Member selected to help recover an owner device into MLS */
  ownerRecoveryHelper?: string;
  /** Time when this owner device requested member-assisted MLS recovery */
  ownerRecoveryRequestedAt?: number;
};

export type IOwnerDeviceRecord = {
  deviceId: string;
  ownerId: string;
  label: string;
  registeredAt: number;
};

export type IChatMessage = {
  /** Unique ID for each message */
  uuid: string;
  /** User who sent the message */
  user: string;
  /** Timestamp when the message was sent */
  ts: number;
  /** Message content */
  message: string;

  /** Cached time string (too expensive) */
  tsStr?: string;
};

export type IChatChannel = {
  /** Channel ID */
  uuid: string;
  /** Channel name */
  name: string;
};

export type IProject = {
  /** Project ID */
  uuid: string;
  /** Project name */
  name: string;
};

export type IProjectFile = {
  /** UUID of file */
  uuid: string;
  /** Full path of file */
  path: string;
  /** Whether the file is a blob */
  is_blob?: boolean;
};

export type AwarenessLocalState = {
  user: {
    name: string;
    color: string;
  };
};

export type IBlobVersion = {
  /** Name of the NDN object */
  name: string;
  /** Timestamp of version */
  time: number;
  /** Size of the blob */
  size: number;
};

export type IProfile = {
  /* NDN name of the user */
  name: string;
  /* OPTIONAL: Email address of the user */
  email?: string;
  /* OPTIONAL: Whether the user is the owner of the workspace */
  owner?: boolean;
};
