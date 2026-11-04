import { createContext, useContext, type ReactNode } from "react";

/** Dialogs opened from navigation, header and home actions. */
export type FeatureName = "github" | "favorites" | "api" | "settings" | "notifications" | "theme" | "profile";

export const FeatureContext = createContext<(name: FeatureName) => void>(() => {});

export interface FeatureButtonProps {
  name: FeatureName;
  children?: ReactNode;
  className?: string;
  label?: string;
}

/** A button that opens the dialog of a feature. */
export function F({ name, children, className = "", label }: FeatureButtonProps) {
  const open = useContext(FeatureContext);
  return (
    <button className={className} onClick={() => open(name)} aria-label={label}>
      {children}
    </button>
  );
}
