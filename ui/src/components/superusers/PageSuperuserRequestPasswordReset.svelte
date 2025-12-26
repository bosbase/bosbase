<script>
    import { _ } from "svelte-i18n";
    import { link } from "svelte-spa-router";
    import ApiClient from "@/utils/ApiClient";
    import FullPage from "@/components/base/FullPage.svelte";
    import Field from "@/components/base/Field.svelte";

    let email = "";
    let isLoading = false;
    let success = false;

    async function submit() {
        if (isLoading) {
            return;
        }

        isLoading = true;

        try {
            await ApiClient.collection("_superusers").requestPasswordReset(email);
            success = true;
        } catch (err) {
            ApiClient.error(err);
        }

        isLoading = false;
    }
</script>

<FullPage>
    {#if success}
        <div class="alert alert-success">
            <div class="icon"><i class="ri-checkbox-circle-line" /></div>
            <div class="content">
                <p>{$_("page.forget.content.2", { values: { email: email } })}</p>
            </div>
        </div>
    {:else}
        <form class="m-b-base" on:submit|preventDefault={submit}>
            <div class="content txt-center m-b-sm">
                <h4 class="m-b-xs">{$_("page.forget.title")}</h4>
                <p>{$_("page.forget.content.1")}</p>
            </div>

            <Field class="form-field required" name="email" let:uniqueId>
                <label for={uniqueId}>{$_("common.user.email")}</label>
                <!-- svelte-ignore a11y-autofocus -->
                <input type="email" id={uniqueId} required autofocus bind:value={email} />
            </Field>

            <button
                type="submit"
                class="btn btn-lg btn-block full-width"
                class:btn-loading={isLoading}
                disabled={isLoading}
            >
                <i class="ri-mail-send-line" />
                <span class="txt">{$_("common.action.sendResetEmail")}</span>
            </button>
        </form>
    {/if}

    <div class="content txt-center">
        <a href="/login" class="link-hint" use:link>{$_("common.action.goToLogin")}</a>
    </div>
</FullPage>
