<script>
    import { _ } from "svelte-i18n";
    import { scale } from "svelte/transition";
    import CommonHelper from "@/utils/CommonHelper";
    import tooltip from "@/actions/tooltip";
    import { errors } from "@/stores/errors";
    import Accordion from "@/components/base/Accordion.svelte";
    import TokenField from "@/components/collections/TokenField.svelte";

    export let collection;

    let tokensList = [];

    $: isSuperusers = collection?.system && collection?.name === "_superusers";

    $: tokensList = isSuperusers
        ? [
              { key: "authToken", label: $_("common.popup.authSetting.name") },
              {
                  key: "passwordResetToken",
                  label: $_("common.popup.authSetting.token.input.passwordResetToken.name"),
              },
              { key: "fileToken", label: $_("common.popup.authSetting.token.input.fileToken.name") },
          ]
        : [
              { key: "authToken", label: $_("common.popup.authSetting.token.input.authToken.name") },
              { key: "verificationToken", label:  $_("common.popup.authSetting.token.input.verificationToken.name") },
              {
                  key: "passwordResetToken",
                  label: $_("common.popup.authSetting.token.input.passwordResetToken.name"),
              },
              { key: "emailChangeToken", label: $_("common.popup.authSetting.token.input.emailChangeToken.name") },
              { key: "fileToken", label: $_("common.popup.authSetting.token.input.fileToken.name") },
          ];

    $: hasErrors = hasTokenError($errors);

    function hasTokenError(errors) {
        if (CommonHelper.isEmpty(errors)) {
            return false;
        }

        for (let token of tokensList) {
            if (errors[token.key]) {
                return true;
            }
        }

        return false;
    }
</script>

<Accordion single>
    <svelte:fragment slot="header">
        <div class="inline-flex">
            <i class="ri-key-2-line"></i>
            <span class="txt">{$_("common.popup.authSetting.token.name")}</span>
        </div>

        <div class="flex-fill" />

        {#if hasErrors}
            <i
                class="ri-error-warning-fill txt-danger"
                transition:scale={{ duration: 150, start: 0.7 }}
                use:tooltip={{ text: "Has errors", position: "left" }}
            />
        {/if}
    </svelte:fragment>

    <div class="grid">
        {#each tokensList as token (token.key)}
            <div class="col-sm-6">
                <TokenField
                    key={token.key}
                    label={token.label}
                    bind:duration={collection[token.key].duration}
                    bind:secret={collection[token.key].secret}
                />
            </div>
        {/each}
    </div>
</Accordion>
