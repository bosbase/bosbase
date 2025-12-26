import { init, addMessages, locale } from "svelte-i18n";

import { localeConfigs } from "./locales";

Object.entries(localeConfigs).forEach(([code, config]) => {
    addMessages(code, config.messages);
});

export const supportedLocales = Object.entries(localeConfigs).map(([code, config]) => ({
    code,
    label: config.label,
}));

export const DEFAULT_LOCALE = "en";
const STORAGE_KEY = "pb.locale";
const SUPPORTED_CODES = Object.keys(localeConfigs);

function normalizeLocale(value) {
    if (!value) {
        return null;
    }

    const lower = value.toLowerCase();
    if (SUPPORTED_CODES.includes(lower)) {
        return lower;
    }

    const short = lower.split("-")[0];
    return SUPPORTED_CODES.includes(short) ? short : null;
}

function getLocaleConfig(code) {
    return localeConfigs[code] || {};
}

function readStoredLocale() {
    if (typeof window === "undefined") {
        return null;
    }

    try {
        return window.localStorage?.getItem(STORAGE_KEY) || null;
    } catch (err) {
        console.warn("Failed to read stored locale preference.", err);
        return null;
    }
}

function writeStoredLocale(value) {
    if (typeof window === "undefined") {
        return;
    }

    try {
        window.localStorage?.setItem(STORAGE_KEY, value);
    } catch (err) {
        console.warn("Failed to persist locale preference.", err);
    }
}

const initialLocale = (() => {
    if (typeof window === "undefined") {
        return DEFAULT_LOCALE;
    }

    const stored = normalizeLocale(readStoredLocale());
    return stored || DEFAULT_LOCALE;
})();

init({
    fallbackLocale: DEFAULT_LOCALE,
    initialLocale,
});

locale.subscribe((value) => {
    if (typeof window === "undefined") {
        return;
    }

    const resolved = normalizeLocale(value) || DEFAULT_LOCALE;
    const { dir = "ltr" } = getLocaleConfig(resolved);

    writeStoredLocale(resolved);

    if (document?.documentElement) {
        document.documentElement.lang = resolved;
        document.documentElement.dir = dir;
    }
});
