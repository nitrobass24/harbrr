import { notifyError, notifySuccess } from "@/lib/notify"

// copyText writes text to the clipboard and only reports success once the write
// actually resolves — navigator.clipboard.writeText can reject (denied permission,
// insecure context), and a fire-and-forget success toast would lie about that.
export async function copyText(text: string, successMessage: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    notifySuccess(successMessage)
  } catch (err) {
    notifyError("Copy failed — select and copy it manually", err)
  }
}
