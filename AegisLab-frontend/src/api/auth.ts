import {
  AuthenticationApi,
  type LoginReq,
  type LoginResp,
  type RegisterReq,
  type TokenRefreshResp,
  type UserDetailResp,
  type UserInfo,
} from '@rcabench/client';

import { sdkAxios, sdkConfig } from './sdk';

const authSdk = new AuthenticationApi(sdkConfig, '', sdkAxios);

export const authApi = {
  login: (data: LoginReq): Promise<LoginResp | undefined> =>
    authSdk.login({ request: data }).then((r) => r.data.data),

  register: (data: RegisterReq): Promise<UserInfo | undefined> =>
    authSdk.registerUser({ request: data }).then((r) => r.data.data),

  logout: () => authSdk.logout().then((r) => r.data),

  getProfile: (): Promise<UserDetailResp> =>
    authSdk.getCurrentUserProfile().then((r) => r.data.data as UserDetailResp),

  changePassword: (data: { old_password: string; new_password: string }) =>
    authSdk
      .changePassword({
        request: {
          old_password: data.old_password,
          new_password: data.new_password,
        },
      })
      .then((r) => r.data),

  refreshToken: (token: string): Promise<TokenRefreshResp> =>
    authSdk
      .refreshAuthToken({ request: { token } })
      .then((r) => r.data.data as TokenRefreshResp),
};
