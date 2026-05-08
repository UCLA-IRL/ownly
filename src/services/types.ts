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
  /** Last access time */
  lastAccess?: number;

  /** Pre-shared key */
  psk: string;
  /** Dynamic-shared key */
  dsk: string | null;
  /** DSK request key */
  dskExch?: string;
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



/**
 * AgentCard describes the metadata exposed by an A2A agent.
 * The shape of this interface matches the common fields in the Agent-to-Agent specification.
 * Additional fields may be added when needed.
 */

export interface IAgentCard {
  /** Human readable name for the agent */
  name: string;
  /** Short description of the agent */
  description: string;
  /** Base URL where the agent is hosted */
  url: string;

  [key: string]: unknown;

}


/** Individual message exchanged in an agent channel. The role field distinguishes between messages sent by the user and those sent by the agent. */
export interface IAgentMessage{
  /** Unique identifier of the message */
  uuid: string;
  /** Identifier of the sender (user name or agent name)*/
  user: string;
  /** timestamp when the message was sent (epoch milliseconds) */
  ts: number;
  /** Content of the message */
  message: string;
  /** The role of the sender ('user' for human, 'agent' for replies) */
  role: 'user' | 'agent';
}



/**
 * A chat channel bound to a specific agent. Each agent channel keeps track of the agent card so calls can be routed correctly.
 */
export interface IAgentChannel {
  /** Unique identifier for the channel */
  uuid: string;
  /** Display name for the channel */
  name: string;
  /** Reference to the agent card by its URL (used as ID) */
  agentId: string;
}

