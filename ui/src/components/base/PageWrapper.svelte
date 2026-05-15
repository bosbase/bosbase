<script>
    import { superuser } from "@/stores/superuser";
    import { _ } from "svelte-i18n";

    export let center = false;

    let classes = "";
    export { classes as class }; // export reserved keyword
</script>

<div class="page-wrapper {classes}" class:center-content={center}>
    <main class="page-content">
        <slot />
    </main>

    <footer class="page-footer">
        <slot name="footer" />

        {#if $superuser?.id}
            <span class="delimiter">|</span>
            {#if import.meta.env.PB_VERSION}
                <a
                    href="https://github.com/bosbase/bosbase-enterprise/releases"
                    target="_blank"
                    rel="noopener noreferrer"
                    title="Releases"
                >
                    <span class="txt">BosBase {import.meta.env.PB_VERSION}</span>
                </a>
            {/if}
        {/if}
    </footer>
</div>
