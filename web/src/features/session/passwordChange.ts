import { HttpError } from "../../api/http";
import { profile } from "../../api/session";

// A refused or failed profile answer only hides the password form; it is not
// a sign-in problem. Network and parsing errors still reach the caller.
export async function passwordChangeAllowed(signal?: AbortSignal): Promise<boolean> {
  try {
    return (await profile(signal)).password_change;
  } catch (error) {
    if (error instanceof HttpError) return false;
    throw error;
  }
}
