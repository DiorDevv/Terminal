import type { useToast } from "./toast";

/** Fields the backend adds to a response after it tried to reload squid. */
export interface ReloadResult {
  reloaded?: boolean;
  reload_error?: string;
  warning?: string;
}

/**
 * Returns a message when the change was saved but squid did not pick it up
 * (reload failed and was rolled back, squid isn't running, ...), else null.
 */
export function reloadFailure(data: ReloadResult | undefined): string | null {
  if (data?.reloaded === false) {
    return data.reload_error ?? "O'zgarish saqlandi, lekin Squid qayta yuklanmadi";
  }
  return data?.warning ?? null;
}

/** Shows an error toast if the reload failed, otherwise the success message. */
export function notifyResult(
  toast: ReturnType<typeof useToast>,
  data: ReloadResult | undefined,
  successMessage: string,
) {
  const failure = reloadFailure(data);
  if (failure) toast.error(failure);
  else toast.success(successMessage);
}
