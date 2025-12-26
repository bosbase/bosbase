import ar from "./ar.json";
import de from "./de.json";
import en from "./en.json";
import es from "./es.json";
import fr from "./fr.json";
import it from "./it.json";
import ja from "./ja.json";
import ko from "./ko.json";
import ru from "./ru.json";
import zh from "./zh.json";

export const localeConfigs = {
    en: {
        label: "English",
        dir: "ltr",
        messages: en,
    },
    zh: {
        label: "中文",
        dir: "ltr",
        messages: zh,
    },
    ar: {
        label: "العربية",
        dir: "rtl",
        messages: ar,
    },
    es: {
        label: "Español",
        dir: "ltr",
        messages: es,
    },
    de: {
        label: "Deutsch",
        dir: "ltr",
        messages: de,
    },
    fr: {
        label: "Français",
        dir: "ltr",
        messages: fr,
    },
    it: {
        label: "Italiano",
        dir: "ltr",
        messages: it,
    },
    ja: {
        label: "日本語",
        dir: "ltr",
        messages: ja,
    },
    ko: {
        label: "한국어",
        dir: "ltr",
        messages: ko,
    },
    ru: {
        label: "Русский",
        dir: "ltr",
        messages: ru,
    },
};
