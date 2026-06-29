export type BundlerOpts = {
  thresholdSize?: number;
  delayMs?: number;
  maxDelayMs?: number;
};

export class Bundler {
  protected _bundle: Array<Uint8Array> = [];
  protected _timerId: NodeJS.Timeout | undefined;
  protected _startTime = 0;
  protected _cumulativeSize = 0;
  protected _opts: Required<BundlerOpts>;

  constructor(
    public readonly merge: (updates: Array<Uint8Array>) => Uint8Array,
    public readonly emit: (update: Uint8Array) => Promise<void>,
    opts: {
      thresholdSize?: number;
      delayMs?: number;
      maxDelayMs?: number;
    } = {},
  ) {
    this._opts = {
      thresholdSize: 3000,
      delayMs: 400, // combine as typing
      maxDelayMs: 1600,
      ...opts,
    };
  }

  public async produce(update: Uint8Array) {
    this._bundle.push(update);
    this._cumulativeSize += update.byteLength;
    if (this._cumulativeSize > this._opts.thresholdSize) {
      // Emit immediately
      await this.issue();
    } else if (!this._timerId) {
      // Schedule emit
      this._timerId = setTimeout(() => this.issue(), this._opts.delayMs);
      this._startTime = Date.now();
    } else {
      // Delay for longer when there is input
      const timePast = Date.now() - this._startTime;
      const maxDelayRemaining = this._opts.maxDelayMs - timePast;
      if (maxDelayRemaining > 10) {
        const nextDelay = Math.min(this._opts.delayMs, maxDelayRemaining);
        clearTimeout(this._timerId);
        this._timerId = setTimeout(() => this.issue(), nextDelay);
      }
    }
  }

  public async issue() {
    if (this._timerId) {
      clearTimeout(this._timerId);
    }
    this._timerId = undefined;
    this._startTime = Date.now();
    const output = this._bundle.length === 1 ? this._bundle[0] : this.merge(this._bundle);
    this._bundle = [];
    this._cumulativeSize = 0;
    await this.emit(output);
  }

  public dispose() {
    if (this._timerId) {
      clearTimeout(this._timerId);
    }
    this._timerId = undefined;
    this._bundle = [];
    this._cumulativeSize = 0;
  }
}
