import { toast } from 'vue3-toastify';
import { themeIsDark } from './theme';

class Handle {
  constructor(private id: string | number) {}

  async msg(message: string) {
    await this.update({ message });
  }

  async success(message: string, timeout = 3000) {
    await this.update({
      message: message,
      type: 'success',
      loading: false,
      timeout: timeout,
      closeOnClick: true,
    });
  }

  async error(message: any) {
    await this.update({
      message: String(message),
      type: 'error',
      loading: false,
      timeout: true,
      closeOnClick: true,
    });
  }

  async warning(message: string) {
    await this.update({
      message: message,
      type: 'warning',
      loading: false,
      timeout: true,
      closeOnClick: true,
    });
  }

  dismiss() {
    toast.remove(this.id);
  }

  async update(opts: {
    message?: string;
    type?: 'success' | 'error' | 'warning' | 'info';
    loading?: boolean;
    timeout?: boolean | number;
    closeOnClick?: boolean;
  }) {
    // Due to a bug in vue3-toastify, we need to wait
    // for the next tick before updating the toast
    await new Promise((resolve) => setTimeout(resolve, 0));

    const toastOpts: any = {
      render: opts.message,
      type: opts.type,
      isLoading: opts.loading,
      autoClose: opts.timeout,
      closeOnClick: opts.closeOnClick,
    };

    // Remove undefined values
    Object.entries(toastOpts).forEach(
      ([key, value]) => value === undefined && delete toastOpts[key],
    );

    // console.log('update', this.id, tOpts);
    toast.update(this.id, toastOpts);
  }
}

export class Toast {
  static Handle = new Handle(0);

  static success(message: string): Handle {
    return new Handle(toast.success(message, Toast.opts()));
  }

  static error(message: any): Handle {
    return new Handle(toast.error(String(message), Toast.opts()));
  }

  static warning(message: string): Handle {
    return new Handle(toast.warning(message, Toast.opts()));
  }

  static info(message: string): Handle {
    return new Handle(toast.info(message, Toast.opts()));
  }

  static async promise(
    promise: Promise<any>,
    opts: { pending: string; success: string; error: string },
  ): Promise<Handle> {
    const handle = this.loading(opts.pending);
    try {
      await promise;
      await handle.success(opts.success);
    } catch (err) {
      await handle.error(opts.error ?? String(err));
      throw err;
    }
    return handle;
  }

  static loading(message: string): Handle {
    return new Handle(toast.loading(message, Toast.opts()));
  }

  private static opts() {
    return {
      theme: themeIsDark() ? 'dark' : 'light',
      position: 'bottom-right',
      transition: {
        // Disable the enter animation because it's buggy during updates.
        // Each update will cause the toast to flicker.
        enter: 'fade-toast--enter--disabled',
        exit: 'fade-toast--leave',
      },
      clearOnUrlChange: true,
    } as {
      theme: 'dark' | 'light';
      position: 'bottom-right';
      transition: {
        enter: string;
        exit: string;
      };
      clearOnUrlChange: boolean;
    };
  }
}
