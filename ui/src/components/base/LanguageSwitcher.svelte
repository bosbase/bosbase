<script>
    import { _, locale } from "svelte-i18n";
    import { DEFAULT_LOCALE, supportedLocales } from "@/i18n";
    import { onDestroy } from "svelte";

    let selected = DEFAULT_LOCALE;

    const unsubscribe = locale.subscribe((value) => {
        selected = value || DEFAULT_LOCALE;
    });

    onDestroy(() => {
        unsubscribe();
    });

    function handleChange(event) {
        locale.set(event.target.value);
    }
</script>

<div class="language-switcher">
    <label class="switcher-label" for="app-language">{$_("common.action.checkLocale")}</label>

    <select id="app-language" class="switcher-select" bind:value={selected} on:change={handleChange}>
        {#each supportedLocales as option}
            <option value={option.code}>{option.label}</option>
        {/each}
    </select>
</div>

<style>
    .language-switcher {
        display: inline-flex;
        align-items: center;
        gap: 0.5rem;
        font-size: 0.85rem;
    }

    .switcher-label {
        color: var(--txtHintColor);
    }

    .switcher-select {
        border-radius: 4px;
        border: 1px solid var(--separatorColor);
        background-color: var(--inputBgColor);
        color: var(--txtColor);
        padding: 4px 8px;
    }
</style>
