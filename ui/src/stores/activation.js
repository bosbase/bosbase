import { writable } from "svelte/store";

const emptyStatus = {
    activated: false,
    activationEmail: "",
    activationMode: "",
    subscriptionExpires: "",
    trialStartedAt: "",
    trialExpiresAt: "",
    isTrial: false,
    isExpired: false,
    requiresActivation: false,
    message: "",
};

export const activationStatus = writable({ ...emptyStatus });

export function setActivationStatus(status = {}) {
    activationStatus.set({ ...emptyStatus, ...status });
}

export function resetActivationStatus() {
    activationStatus.set({ ...emptyStatus });
}
