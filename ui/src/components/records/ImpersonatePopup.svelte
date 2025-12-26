<script>
    import { _ } from "svelte-i18n";
    import { createEventDispatcher } from "svelte";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { addSuccessToast } from "@/stores/toasts";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import CopyIcon from "@/components/base/CopyIcon.svelte";
    import SdkTabs from "@/components/base/SdkTabs.svelte";
    import Field from "@/components/base/Field.svelte";

    const dispatch = createEventDispatcher();
    const formId = "impersonate_" + CommonHelper.randomString(5);

    export let collection;
    export let record;

    let panel;
    let duration = 0;
    let isSubmitting = false;
    let impersonateClient;

    $: backendAbsUrl = CommonHelper.getApiExampleUrl(impersonateClient?.baseURL);

    export function show() {
        if (!record) {
            return;
        }
        reset();
        panel?.show();
    }

    export function hide() {
        panel?.hide();
        reset();
    }

    async function submit() {
        if (isSubmitting || !collection || !record) {
            return;
        }

        isSubmitting = true;

        try {
            impersonateClient = await ApiClient.collection(collection.name).impersonate(record.id, duration);

            dispatch("submit", impersonateClient);
        } catch (err) {
            ApiClient.error(err);
        }

        isSubmitting = false;
    }

    function reset() {
        duration = 0;
        impersonateClient = undefined;
    }
</script>

<OverlayPanel
    bind:this={panel}
    overlayClose={false}
    escClose={!isSubmitting}
    beforeHide={() => !isSubmitting}
    popup
    on:show
    on:hide
>
    <svelte:fragment slot="header">
        <h4>{$_("common.popup.mockNewToken.name")}</h4>
    </svelte:fragment>

    <div class="clearfix"></div>

    {#if impersonateClient?.authStore?.token}
        <div class="alert alert-success">
            <div class="content txt-bold">
                <span class="txt token-holder">{impersonateClient.authStore.token}</span>
                <CopyIcon value={impersonateClient.authStore.token} />
            </div>
        </div>

        <SdkTabs
            class="m-b-0"
            js={`
                import BosBase from 'bosbase';

                const token = "...";

                const pb = new BosBase('${backendAbsUrl}');

                pb.authStore.save(token, null);
            `}
            dart={`
                import 'package:bosbase/bosbase.dart';

                final token = "...";

                final pb = BosBase('${backendAbsUrl}');

                pb.authStore.save(token, null);
            `}
        />
    {:else}
        <form id={formId} on:submit|preventDefault={submit}>
            <div class="content">
                <p>
                    {$_("common.popup.mockNewToken.content.1", {
                        values: { email: CommonHelper.displayValue(record) },
                    })}
                </p>
            </div>

            <Field class="form-field m-b-xs m-t-sm" name="duration" let:uniqueId>
                <label for={uniqueId}>Token duration (in seconds)</label>
                <input
                    type="number"
                    id={uniqueId}
                    placeholder={$_("common.message.defaultValue", {
                        values: { value: collection?.authToken?.duration || 0 + "s" },
                    })}
                    min="0"
                    step="1"
                    value={duration || ""}
                    on:input={(e) => (duration = e.target.value << 0)}
                />
            </Field>
        </form>
    {/if}

    <svelte:fragment slot="footer">
        <button type="button" class="btn btn-transparent" on:click={hide} disabled={isSubmitting}>
            <span class="txt">{$_("common.action.close")}</span>
        </button>
        {#if impersonateClient?.authStore?.token}
            <button
                type="button"
                class="btn btn-secondary btn-expanded"
                disabled={isSubmitting}
                on:click={() => reset()}
            >
                <span class="txt">Generate a new one</span>
            </button>
        {:else}
            <button
                type="submit"
                form={formId}
                class="btn btn-expanded"
                class:btn-loading={isSubmitting}
                disabled={isSubmitting}
                on:click={() => submit()}
            >
                <span class="txt">Generate token</span>
            </button>
        {/if}
    </svelte:fragment>
</OverlayPanel>

<style>
    .token-holder {
        user-select: all;
    }
</style>
