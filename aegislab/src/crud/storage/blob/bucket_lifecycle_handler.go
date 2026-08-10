package blob

import (
	"errors"
	"net/http"

	"aegis/platform/dto"

	"github.com/gin-gonic/gin"
)

// GetBucketLifecycle returns the current lifecycle policy for a bucket.
// Empty rules list when no policy has been set.
//
//	@Summary		Get bucket lifecycle policy
//	@Description	Return the lifecycle policy persisted for the bucket. Empty rules list when no policy is set. Execution is deferred — see lifecycleExecutionDeferred.
//	@Tags			Blob
//	@ID				blob_get_bucket_lifecycle
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string										true	"Bucket name"
//	@Success		200		{object}	dto.GenericResponse[BucketLifecycle]		"Lifecycle policy"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]					"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]					"Bucket not found"
//	@Router			/api/v2/blob/buckets/{bucket}/lifecycle [get]
//	@x-api-type		{"portal":"true","sdk":"true","admin":"true"}
func (h *Handler) GetBucketLifecycle(c *gin.Context) {
	bucket := c.Param("bucket")
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	sub := subjectFromContext(c)
	if !h.auth.CanWrite(&b.Config, sub) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return
	}
	lc := b.Config.Lifecycle
	if lc == nil {
		lc = &BucketLifecycle{Rules: []BucketLifecycleRule{}}
	}
	dto.SuccessResponse(c, lc)
}

// PutBucketLifecycle replaces a bucket's lifecycle policy. An empty
// rules list clears the policy (equivalent to never having set one).
//
//	@Summary		Replace bucket lifecycle policy
//	@Description	Replace the lifecycle policy for a bucket. Empty rules clears the policy. Static-config buckets cannot be mutated at runtime.
//	@Tags			Blob
//	@ID				blob_put_bucket_lifecycle
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			bucket	path		string										true	"Bucket name"
//	@Param			request	body		BucketLifecycle								true	"New lifecycle policy"
//	@Success		200		{object}	dto.GenericResponse[BucketLifecycle]		"Lifecycle updated"
//	@Failure		400		{object}	dto.GenericResponse[any]					"Invalid policy"
//	@Failure		401		{object}	dto.GenericResponse[any]					"Authentication required"
//	@Failure		403		{object}	dto.GenericResponse[any]					"Forbidden"
//	@Failure		404		{object}	dto.GenericResponse[any]					"Bucket not found"
//	@Failure		500		{object}	dto.GenericResponse[any]					"Internal server error"
//	@Router			/api/v2/blob/buckets/{bucket}/lifecycle [put]
//	@x-api-type		{"portal":"true","sdk":"true","admin":"true"}
func (h *Handler) PutBucketLifecycle(c *gin.Context) {
	bucket := c.Param("bucket")
	b, err := h.svc.Registry().Lookup(bucket)
	if err != nil {
		dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		return
	}
	sub := subjectFromContext(c)
	if !h.auth.CanWrite(&b.Config, sub) {
		dto.ErrorResponse(c, http.StatusForbidden, ErrUnauthorized.Error())
		return
	}
	var body BucketLifecycle
	if err := c.ShouldBindJSON(&body); err != nil {
		dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.Registry().SetLifecycle(c.Request.Context(), bucket, &body); err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			dto.ErrorResponse(c, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrBucketLifecycleInvalid):
			dto.ErrorResponse(c, http.StatusBadRequest, err.Error())
		default:
			dto.ErrorResponse(c, http.StatusInternalServerError, err.Error())
		}
		return
	}
	dto.SuccessResponse(c, &body)
}
