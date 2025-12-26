<script>
    import { _ } from "svelte-i18n";
    import { createEventDispatcher, onMount } from "svelte";
    import tooltip from "@/actions/tooltip";

    const dispatch = createEventDispatcher();

    let tooltipData = { text: $_("common.action.refresh"), position: "right" };
    export { tooltipData as tooltip };

    let classes = "";
    export { classes as class };

    let refreshTimeoutId = null;

    function refresh() {
        dispatch($_("common.action.refresh"));

        // clear tooltip
        const oldTooltipData = tooltipData;
        tooltipData = null;

        clearTimeout(refreshTimeoutId);
        refreshTimeoutId = setTimeout(() => {
            refreshTimeoutId = null;
            tooltipData = oldTooltipData;
        }, 150);
    }

    onMount(() => {
        return () => clearTimeout(refreshTimeoutId);
    });
</script>

<button
    type="button"
    aria-label="Refresh"
    class="btn btn-transparent btn-circle {classes}"
    class:refreshing={refreshTimeoutId}
    use:tooltip={tooltipData}
    on:click={refresh}
>
    <i class="ri-refresh-line" />
</button>

<style>
    @keyframes refresh {
        100% {
            transform: rotate(180deg);
        }
    }
    .action.refreshing i {
        animation: refresh 150ms ease-out;
    }
</style>
