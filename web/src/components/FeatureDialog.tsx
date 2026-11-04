import { useEffect, useRef, type ReactNode } from "react";
import type { FeatureName } from "./FeatureContext";
import { I } from "./Icon";

const descriptions: Record<FeatureName, [title: string, text: string]> = {
  github: [
    "GitHub",
    "Управление подключениями GitHub",
  ],
  favorites: [
    "Избранное",
    "Избранное ещё не настроено. Можно пользоваться закладками браузера.",
  ],
  api: [
    "API и MCP",
    "Статьи создаются и обновляются через REST API или MCP. Веб-интерфейс показывает статьи и позволяет управлять подключениями GitHub; статьи хранятся локально.",
  ],
  settings: [
    "Настройки",
    "Приватный источник доступен только с авторизацией. Публичные статьи видны без входа.",
  ],
  notifications: [
    "Уведомления",
    "Внутренний центр уведомлений VEDA пока не настроен.",
  ],
  theme: [
    "Оформление",
    "Активна тёмная тема VEDA. Переключение тем пока недоступно.",
  ],
  profile: [
    "Профиль",
    "Вход в учётную запись VEDA и смена пароля.",
  ],
};

export interface FeatureDialogProps {
  feature: FeatureName | null;
  isPublic: boolean;
  /** Feature bodies supplied by the composition root; others show their description. */
  bodies: Partial<Record<FeatureName, ReactNode>>;
  onClose: () => void;
}

// One modal dialog shows whichever feature was opened last.
export function FeatureDialog({ feature, isPublic, bodies, onClose }: FeatureDialogProps) {
  const modal = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (feature) modal.current?.showModal();
    else modal.current?.close();
  }, [feature]);
  const description = feature ? descriptions[feature] : undefined;
  return (
    <dialog
      ref={modal}
      onCancel={onClose}
      onClick={(e) => {
        if (e.target === modal.current) onClose();
      }}
    >
      <div className="dialog-head">
        <h2>{feature === "profile" && isPublic ? "Вход" : description?.[0] || "VEDA"}</h2>
        <button aria-label="Закрыть" onClick={onClose}>
          <I name="x" />
        </button>
      </div>
      {(feature && bodies[feature]) || <p>{description?.[1]}</p>}
    </dialog>
  );
}
