<script>
    import { _ } from 'svelte-i18n';
    import { json } from "svelte-i18n";
    import { onMount } from "svelte";
    import { pageTitle } from "@/stores/app";

    $pageTitle = $_("common.menu.oauth2");

    onMount(() => {
        window.close();
    });
</script>

<div class="content txt-hint txt-center p-base">
    <h3 class="m-b-sm">{$_("common.message.oauth2AuthFailed")}</h3>
    <h5>{$_("common.message.plaseTryAgain")}</h5>
</div>
